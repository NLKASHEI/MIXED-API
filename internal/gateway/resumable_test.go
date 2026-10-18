package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpressDownstreamResumeDoesNotGenerateTwice(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "private-key", Auth: "none", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	setting := `{"max_retries":0,"request_timeout_seconds":10,"aggregate_stream":false,"resumable_enabled":true,"resumable_ttl_seconds":60}`
	if w := accountTestCall(app, "PUT", "/api/engine-settings/vertex", "admin", setting); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var calls atomic.Int32
	release := make(chan struct{})
	defer app.CloseResumable()
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")
			select {
			case <-release:
				io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"world\"}]},\"finishReason\":\"STOP\"}]}\n\n")
			case <-r.Context().Done():
			}
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
	})
	server := httptest.NewServer(app)
	defer server.Close()
	input := `{"model":"vertex/gemini-test","stream":true,"messages":[{"role":"user","content":"hello"}]}`
	request := func(ctx context.Context) *http.Request {
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader(input))
		r.Header.Set("Authorization", "Bearer client")
		r.Header.Set("Idempotency-Key", "one-turn")
		return r
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := http.DefaultClient.Do(request(ctx))
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Header.Get("X-Mixed-Stream-ID")
	if id == "" {
		resp.Body.Close()
		t.Fatal("no resumable stream ID")
	}
	reader := bufio.NewReader(resp.Body)
	var first strings.Builder
	for {
		line, e := reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		first.WriteString(line)
		if line == "\n" && strings.Contains(first.String(), "data:") {
			break
		}
	}
	if !strings.Contains(first.String(), "hello") {
		t.Fatal(first.String())
	}
	for i := 0; i < 2; i++ {
		r := request(context.Background())
		r.Header.Del("Idempotency-Key")
		other, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		other.Body.Close()
		if other.Header.Get("X-Mixed-Stream-ID") == id {
			t.Fatal("concurrent request merged")
		}
	}
	conflict := request(context.Background())
	conflict.Body = io.NopCloser(strings.NewReader(strings.Replace(input, "hello", "different", 1)))
	conflict.ContentLength = -1
	rejected, err := http.DefaultClient.Do(conflict)
	if err != nil {
		t.Fatal(err)
	}
	rejected.Body.Close()
	if rejected.StatusCode != 409 {
		t.Fatal("idempotency conflict started generation", rejected.StatusCode)
	}
	cancel()
	resp.Body.Close()
	// A reader disconnect must not cancel the single buffered upstream.
	w := accountTestCall(app, "GET", "/v1/resumable/"+id+"?from=1&wait=0", "client", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	var poll struct {
		Events []struct {
			Seq  int    `json:"seq"`
			Data string `json:"data"`
		} `json:"events"`
		Done bool `json:"done"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w = accountTestCall(app, "GET", "/v1/resumable/"+id+"?from=1&wait=1", "client", "")
		if json.Unmarshal(w.Body.Bytes(), &poll) != nil {
			t.Fatal(w.Body.String())
		}
		if poll.Done {
			break
		}
	}
	if !poll.Done || len(poll.Events) == 0 || strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), "world") {
		t.Fatal(w.Body.String())
	}
	replay, err := http.DefaultClient.Do(request(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(replay.Body)
	replay.Body.Close()
	for i := 0; i < 2; i++ {
		r := request(context.Background())
		r.Header.Del("Idempotency-Key")
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.Header.Get("X-Mixed-Stream-ID") == id {
			t.Fatal("independent request reused previous generation")
		}
	}
	if calls.Load() != 5 {
		t.Fatal("independent requests merged", calls.Load())
	}
	if calls.Load() != 5 || replay.Header.Get("X-Mixed-Stream-ID") != id || !strings.Contains(string(raw), "[DONE]") {
		t.Fatal("resume regenerated", calls.Load(), string(raw))
	}
	reconnected, _ := http.NewRequest("GET", server.URL+"/v1/resumable/"+id, nil)
	reconnected.Header.Set("Authorization", "Bearer client")
	reconnected.Header.Set("Accept", "text/event-stream")
	reconnected.Header.Set("Last-Event-ID", "0")
	continued, err := http.DefaultClient.Do(reconnected)
	if err != nil {
		t.Fatal(err)
	}
	continuedBody, _ := io.ReadAll(continued.Body)
	continued.Body.Close()
	if continued.StatusCode != 200 || strings.Contains(string(continuedBody), "hello") || !strings.Contains(string(continuedBody), "world") {
		t.Fatal(string(continuedBody))
	}
	if denied := accountTestCall(app, "GET", "/v1/resumable/"+id, "wrong-key", ""); denied.Code != 401 {
		t.Fatal("resume leaked without authentication")
	}
}

func TestResumablePreservesRejectionAndExplicitCancellation(t *testing.T) {
	for _, reject := range []bool{true, false} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.AdminKey = "admin"
		cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
		cfg.EngineSettings = map[string]engineSettings{"vertex": {MaxRetries: 0, RequestTimeoutSeconds: 10, ResumableEnabled: true}}
		app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
		defer app.CloseResumable()
		cancelled := make(chan struct{})
		app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if reject {
				return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": {"7"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"status":"RESOURCE_EXHAUSTED"}}`))}, nil
			}
			reader, writer := io.Pipe()
			go func() {
				defer writer.Close()
				io.WriteString(writer, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}\n\n")
				<-r.Context().Done()
				close(cancelled)
			}()
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: reader}, nil
		})
		server := httptest.NewServer(app)
		req, _ := http.NewRequest("POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"vertex/gemini-test","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer client")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		id := response.Header.Get("X-Mixed-Stream-ID")
		response.Body.Close()
		if reject {
			if response.StatusCode != 429 || response.Header.Get("Retry-After") != "7" {
				t.Fatal(response.StatusCode, response.Header)
			}
		} else {
			w := accountTestCall(app, "DELETE", "/v1/resumable/"+id, "client", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("DELETE did not cancel upstream")
			}
		}
		server.Close()
	}
}
