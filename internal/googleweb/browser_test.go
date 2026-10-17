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
	mu           sync.Mutex
	cancelled    int
	polls        int
	closed       bool
	refuseCancel bool
	cancelReply  json.RawMessage
}

func (p *testPage) Eval(ctx context.Context, js string) (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Contains(js, "state()") {
		return json.RawMessage(`null`), nil
	}
	if strings.Contains(js, "cancel(") {
		if p.refuseCancel {
			return nil, errors.New("window unresponsive")
		}
		p.cancelled++
		if p.cancelReply != nil {
			return p.cancelReply, nil
		}
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

func TestUnconfirmedCancellationCannotReuseSession(t *testing.T) {
	for _, reply := range []string{"false", "null"} {
		p := &testPage{cancelReply: json.RawMessage(reply)}
		b := New(func(context.Context, string) (Page, error) { return p, nil })
		if err := b.Open(context.Background(), "aistudio"); err != nil {
			t.Fatal(err)
		}
		r, err := b.Send(context.Background(), "aistudio", Operation{Kind: "build", Model: "test"})
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if !p.closed || b.State(context.Background(), "aistudio").Status != "none" {
			t.Errorf("cancel reply %s allowed another request into unconfirmed page", reply)
		}
		b.Close()
	}
}

func TestFailedCancellationClosesPageAndAllowsExplicitReopen(t *testing.T) {
	p := &testPage{refuseCancel: true}
	created := 0
	b := New(func(context.Context, string) (Page, error) {
		created++
		if created == 1 {
			return p, nil
		}
		return &testPage{}, nil
	})
	defer b.Close()
	if err := b.Open(context.Background(), "aps"); err != nil {
		t.Fatal(err)
	}
	r, err := b.Send(context.Background(), "aps", Operation{Kind: "aps", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if !p.closed {
		t.Fatal("failed cancellation left official fetch alive")
	}
	if state := b.State(context.Background(), "aps"); state.Status != "none" {
		t.Fatalf("broken session reused: %+v", state)
	}
	if err = b.Open(context.Background(), "aps"); err != nil || created != 2 {
		t.Fatalf("reopen: %d %v", created, err)
	}
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

type initializingPage struct {
	testPage
	initialized, stopped bool
}

func (p *initializingPage) Eval(ctx context.Context, js string) (json.RawMessage, error) {
	if strings.Contains(js, "initialize()") {
		p.initialized = true
		return json.RawMessage(`{"status":"initializing"}`), nil
	}
	if strings.Contains(js, "cancelInitialize()") {
		p.stopped = true
		return json.RawMessage(`true`), nil
	}
	if strings.Contains(js, "state()") {
		return json.RawMessage(`{"status":"initializing"}`), nil
	}
	return p.testPage.Eval(ctx, js)
}
func TestInitializationCancellationReleasesLease(t *testing.T) {
	page := &initializingPage{}
	b := New(func(context.Context, string) (Page, error) { return page, nil })
	defer b.Close()
	if err := b.Open(context.Background(), "aistudio"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	state := b.Initialize(ctx, "aistudio")
	if state.Status != "waiting" || !page.initialized || !page.stopped {
		t.Fatalf("cancel state: %+v", state)
	}
	request, cancelRequest := context.WithTimeout(context.Background(), time.Second)
	defer cancelRequest()
	response, err := b.Send(request, "aistudio", Operation{Kind: "models"})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
}
