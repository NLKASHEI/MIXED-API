package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

type autoConnectPage struct {
	allowed, initialized         atomic.Bool
	initializations, generations atomic.Int32
}

func (p *autoConnectPage) Eval(context.Context, string) (json.RawMessage, error) {
	panic("typed interface required")
}
func (p *autoConnectPage) Show()        {}
func (p *autoConnectPage) Close() error { return nil }
func (p *autoConnectPage) Call(_ context.Context, method string, _ ...any) (json.RawMessage, error) {
	switch method {
	case "state":
		if p.initialized.Load() {
			return json.RawMessage(`{"status":"ready"}`), nil
		}
		if p.allowed.Load() {
			return json.RawMessage(`{"status":"waiting","can_initialize":true}`), nil
		}
		return json.RawMessage(`{"status":"waiting"}`), nil
	case "initialize":
		p.initializations.Add(1)
		p.initialized.Store(true)
		return json.RawMessage(`{"status":"ready"}`), nil
	case "start":
		p.generations.Add(1)
	}
	return json.RawMessage(`true`), nil
}
func TestBrowserLoginWaitsForUserThenConnectsWithoutAnotherAction(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.AdminKey = "admin"
	page := &autoConnectPage{}
	browser := googleweb.New(func(context.Context, string) (googleweb.Page, error) { return page, nil })
	defer browser.Close()
	app := NewServer("", cfg)
	app.SetGoogleSessions(browser)
	if w := accountTestCall(app, "POST", "/api/accounts/aps/login", "client", ""); w.Code != 401 {
		t.Fatal("client key authorized browser", w.Code)
	}
	if w := accountTestCall(app, "POST", "/api/accounts/aps/login", "admin", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	time.Sleep(150 * time.Millisecond)
	if page.initializations.Load() != 0 {
		t.Fatal("initialized before user finished official page")
	}
	page.allowed.Store(true)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := accountTestCall(app, "GET", "/api/accounts/aps", "admin", "")
		if strings.Contains(w.Body.String(), `"status":"ready"`) {
			if page.initializations.Load() != 1 || page.generations.Load() != 0 {
				t.Fatal("duplicate initialization or unsolicited generation")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("official page was ready but connection still required a second button")
}
