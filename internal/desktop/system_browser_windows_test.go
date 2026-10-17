//go:build windows

package desktop

import (
	"context"
	"encoding/json"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// Actual browser acceptance uses an owned blank profile and a local fixture;
// it never reads an existing browser profile or signs in to an account.
func TestSystemBrowserPageLifecycle(t *testing.T) {
	if os.Getenv("MIXED_BROWSER_ACCEPTANCE") != "1" {
		t.Skip("explicit desktop browser acceptance")
	}
	exe, err := defaultBrowserExecutable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<title>MIXED browser test</title><h1>Browser fixture</h1>"))
	}))
	defer fixture.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	profile := t.TempDir()
	p, err := launchSystemBrowser(ctx, exe, profile, fixture.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	raw, err := p.Eval(ctx, "({title:document.title,origin:location.origin})")
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Title, Origin string }
	if json.Unmarshal(raw, &got) != nil || got.Title != "MIXED browser test" || got.Origin != fixture.URL {
		t.Fatalf("page not connected: %s", raw)
	}
	// Close only the authorized target while another owned window is still open.
	if err = chromedp.Run(p.ctx, chromedp.ActionFunc(func(c context.Context) error {
		_, err := target.CreateTarget("about:blank").Do(c)
		if err != nil {
			return err
		}
		return target.CloseTarget(chromedp.FromContext(p.ctx).Target.TargetID).Do(cdp.WithExecutor(c, chromedp.FromContext(p.ctx).Browser))
	})); err != nil {
		t.Fatal(err)
	}
	if p.Alive() {
		t.Fatal("closed target reported alive while another window remained")
	}
	p.cancel() // Simulate a lost page/session before cleanup.
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
	if endpointAlive(p.endpoint) {
		t.Fatal("owned browser survived close after cancellation")
	}
	if _, err = p.Eval(ctx, "true"); err == nil {
		t.Fatal("closed browser accepted work")
	}
	reopened, err := launchSystemBrowser(ctx, exe, profile, fixture.URL)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	reopened.Close()
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err = launchSystemBrowser(cancelled, exe, profile, fixture.URL); err == nil {
		t.Fatal("cancelled startup accepted")
	}
}

// Opt-in source-page markup probe uses a new empty profile; no account login.
func TestOfficialPageMarkup(t *testing.T) {
	id := os.Getenv("MIXED_SITE_PROBE")
	if id != "aps" && id != "aistudio" {
		t.Skip("explicit source page probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := newSystemBrowserPage(ctx, id, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	select {
	case <-time.After(8 * time.Second):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	raw, err := p.Eval(ctx, `({path:location.pathname,title:document.title,state:window.__mixedGoogleWeb?.state(),fields:[...document.querySelectorAll('textarea,[contenteditable],input[type=text]')].map(e=>({tag:e.tagName,role:e.getAttribute('role'),editable:e.getAttribute('contenteditable'),testid:e.getAttribute('data-testid'),visible:!!e.getClientRects().length,placeholder:e.getAttribute('placeholder'),label:e.getAttribute('aria-label'),parent:e.parentElement?.tagName})),buttons:[...document.querySelectorAll('button')].filter(e=>e.getClientRects().length).slice(0,35).map(e=>({label:e.getAttribute('aria-label'),text:e.textContent?.slice(0,100),disabled:e.disabled}))})`)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}
