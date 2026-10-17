package googleweb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type testPage struct {
	mu        sync.Mutex
	cancelled int
	polls     int
	closed    bool
}

func (p *testPage) Eval(ctx context.Context, js string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(js, "state()") {
		return json.RawMessage(`null`), nil
	}
	if strings.Contains(js, "cancel(") {
		p.cancelled++
		return json.RawMessage(`true`), nil
	}
	if strings.Contains(js, "start(") {
		p.polls = 0
		return json.RawMessage(`true`), nil
	}
	p.polls++
	if p.polls == 1 {
		return json.RawMessage(`{"status":200,"chunk":"aGk=","done":true}`), nil
	}
	return nil, errors.New("extra poll")
}
func (*testPage) Show()          {}
func (p *testPage) Close() error { p.mu.Lock(); defer p.mu.Unlock(); p.closed = true; return nil }
func TestSessionNullStateAndRequestLease(t *testing.T) {
	p := &testPage{}
	b := New(func(context.Context, string) (Page, error) { return p, nil })
	defer b.Close()
	if err := b.Open(context.Background(), "aistudio"); err != nil {
		t.Fatal(err)
	}
	if state := b.State(context.Background(), "aistudio"); state.Status != "waiting" {
		t.Fatalf("login document state: %+v", state)
	}
	r, err := b.Send(context.Background(), "aistudio", Operation{Kind: "build", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil || string(raw) != "hi" {
		t.Fatalf("%q %v", raw, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err = b.Send(ctx, "aistudio", Operation{Kind: "models"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("concurrent proof was not queued: %v", err)
	}
	r.Body.Close()
	r.Body.Close()
	r, err = b.Send(context.Background(), "aistudio", Operation{Kind: "models"})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if p.cancelled != 2 {
		t.Fatalf("cancel count %d", p.cancelled)
	}
}
func TestStartingWindowDoesNotBlockOtherSessions(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	b := New(func(ctx context.Context, id string) (Page, error) {
		if id == "aistudio" {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &testPage{}, nil
	})
	defer b.Close()
	done := make(chan struct{})
	go func() { defer close(done); b.Open(context.Background(), "aistudio") }()
	<-started
	observed := make(chan struct{})
	go func() { b.State(context.Background(), "aps"); close(observed) }()
	select {
	case <-observed:
	case <-time.After(100 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("starting window held global session lock")
	}
	close(release)
	<-done
}
