package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// Retry rejections and incomplete streams only before delivery to the client.
// Aggregation explicitly buffers a full attempt before making it visible.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	delay := time.Duration(1<<min(attempt, 5)) * time.Second
	if raw := resp.Header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			delay = time.Duration(min(seconds, 86400)) * time.Second
		} else if deadline, err := http.ParseTime(raw); err == nil {
			delay = max(0, time.Until(deadline))
		}
	}
	return delay
}

func (s *Server) expressJSON(r *http.Request, endpoint string, body any, headers map[string]string, stream bool, expected int) (*http.Response, error) {
	settings := s.sourceSettings("vertex")
	for attempt := 0; ; attempt++ {
		resp, err := providerJSON(s.client, r, endpoint, body, headers)
		if err != nil {
			return resp, err
		}
		retryStatus := resp.StatusCode
		var attemptErr error
		if stream && resp.StatusCode == 200 {
			retryable, readErr := prepareGoogleStream(r.Context(), resp, settings.AggregateStream, expected)
			if readErr == nil {
				return resp, nil
			}
			diagnostic(r).set(func(d *RequestDetails) {
				d.ErrorType = "upstream_stream"
				d.Cause = "Google 流在交付前中断"
				d.Hint = "检查实际代理；可开启完整缓存后交付。已交付的内容不会自动重放。"
			})
			if !retryable || attempt >= settings.MaxRetries {
				resp.Body.Close()
				return nil, readErr
			}
			retryStatus = 502
			attemptErr = readErr
		} else if attempt >= settings.MaxRetries || resp.StatusCode != 429 && resp.StatusCode != 502 && resp.StatusCode != 503 {
			return resp, nil
		}
		delay := retryDelay(resp, attempt)
		// Preserve the last HTTP error when the service asks us to wait longer
		// than the remaining request budget; don't turn it into a vague 502.
		if deadline, ok := r.Context().Deadline(); ok && time.Until(deadline) <= delay {
			if attemptErr != nil {
				resp.Body.Close()
				return nil, attemptErr
			}
			return resp, nil
		}
		diagnostic(r).set(func(d *RequestDetails) { d.RetryStatus = retryStatus; d.Stage = "等待重试" })
		resp.Body.Close()
		if err := waitRetry(r.Context(), delay); err != nil {
			return nil, err
		}
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
