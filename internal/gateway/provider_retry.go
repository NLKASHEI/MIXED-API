package gateway

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// A retry only follows an explicit rejection before generation. A network
// failure or a partial stream must never replay a user's prompt.
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

func (s *Server) expressJSON(r *http.Request, endpoint string, body any, headers map[string]string) (*http.Response, error) {
	settings := s.sourceSettings("vertex")
	for attempt := 0; ; attempt++ {
		resp, err := providerJSON(s.client, r, endpoint, body, headers)
		if err != nil || attempt >= settings.MaxRetries || resp.StatusCode != 429 && resp.StatusCode != 503 {
			return resp, err
		}
		delay := retryDelay(resp, attempt)
		// Preserve the last HTTP error when the service asks us to wait longer
		// than the remaining request budget; don't turn it into a vague 502.
		if deadline, ok := r.Context().Deadline(); ok && time.Until(deadline) <= delay {
			return resp, nil
		}
		diagnostic(r).set(func(d *RequestDetails) { d.RetryStatus = resp.StatusCode; d.Stage = "等待重试" })
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
