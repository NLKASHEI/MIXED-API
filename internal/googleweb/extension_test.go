package googleweb

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

func TestCurrentBrowserExtensionTransport(t *testing.T) {
	var opened string
	root := t.TempDir()
	p, err := NewExtensionPage(root, "aistudio", func(u string) error { opened = u; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(opened)
	if u.Hostname() != "127.0.0.1" || u.Fragment == "" {
		t.Fatal("not a local pairing URL")
	}
	if _, err = os.Stat(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	state, err := p.Call(context.Background(), "state")
	if err != nil || !strings.Contains(string(state), "waiting") {
		t.Fatal(string(state), err)
	}
	endpoint := "ws://" + u.Host + "/mixed-browser/socket?token=" + u.Fragment
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := ws.Dialer{Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": []string{"chrome-extension://abcdefghijklmnopabcdefghijklmnop"}})}
	conn, _, _, err := dial.Dial(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			raw, _, e := wsutil.ReadServerData(conn)
			if e != nil {
				return
			}
			var msg struct {
				ID     uint64 `json:"id"`
				Kind   string `json:"kind"`
				Method string `json:"method"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				return
			}
			if msg.Kind == "hello" {
				wsutil.WriteClientText(conn, []byte(`{"ready":true}`))
				continue
			}
			result := `true`
			if msg.Method == "state" {
				result = `{"status":"ready"}`
			}
			if msg.Method == "read" {
				result = `{"status":200,"chunk":"aGk=","done":true}`
			}
			reply, _ := json.Marshal(map[string]any{"id": msg.ID, "result": json.RawMessage(result)})
			if wsutil.WriteClientText(conn, reply) != nil {
				return
			}
		}
	}()
	b := New(func(context.Context, string) (Page, error) { return p, nil })
	defer b.Close()
	if err = b.Open(ctx, "aistudio"); err != nil {
		t.Fatal(err)
	}
	for b.State(ctx, "aistudio").Status != "ready" {
		if ctx.Err() != nil {
			t.Fatal("extension never connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := b.Send(ctx, "aistudio", Operation{Kind: "build", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "hi" {
		t.Fatal(string(body), err)
	}
	if _, err = p.Call(ctx, "eval", "document.cookie"); err == nil {
		t.Fatal("arbitrary execution allowed")
	}
	conn.Close()
	<-done
}

func TestExtensionRejectsForeignOriginAndWrongToken(t *testing.T) {
	var opened string
	p, err := NewExtensionPage(t.TempDir(), "aps", func(s string) error { opened = s; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	u, _ := url.Parse(opened)
	for _, tc := range []struct{ origin, token string }{{"https://attacker.example", u.Fragment}, {"null", u.Fragment}, {"chrome-extension://abcdefghijklmnopabcdefghijklmnop", "wrong"}} {
		d := ws.Dialer{Header: ws.HandshakeHeaderHTTP(http.Header{"Origin": []string{tc.origin}})}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, _, _, err := d.Dial(ctx, "ws://"+u.Host+"/mixed-browser/socket?token="+tc.token)
		cancel()
		if err == nil {
			c.Close()
			t.Fatal("unauthorized extension accepted")
		}
	}
}
