package gateway

import (
	"context"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAISLocalSchemaReferencesRetainConstraints(t *testing.T) {
	for _, channel := range []string{"build", "playground"} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.Backends = []Backend{{ID: "aistudio", Kind: "builtin", URL: builtinBase("aistudio"), Enabled: true}}
		app := NewServer("", cfg)
		calls := 0
		app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(_ context.Context, _ string, op googleweb.Operation) (*http.Response, error) {
			calls++
			if channel=="build" && !strings.Contains(string(op.Body),"parametersJsonSchema") {t.Error("Build tool JSON schema sent through restricted parameters field")}
			if !strings.Contains(string(op.Body), "fixed") {
				t.Error("enum constraint lost", string(op.Body))
			}
			if channel == "playground" && strings.Contains(string(op.Body), "$ref") {
				t.Error("unresolved reference in protobuf schema")
			}
			raw := `["{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}"]`
			if channel == "playground" {
				raw = `[[[[[[[[null,"ok"]],"model"],1]],null,[2,1,3]]]]`
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(raw))}, nil
		}})
		schema := `{"type":"object","$defs":{"answer":{"type":"string","enum":["fixed"]}},"properties":{"answer":{"$ref":"#/$defs/answer"}},"required":["answer"]}`
		body := `{"model":"aistudio/` + channel + `/gemini-test","messages":[{"role":"user","content":"hello"}],"response_format":{"type":"json_schema","json_schema":{"name":"result","schema":` + schema + `}},"tools":[{"type":"function","function":{"name":"lookup","parameters":`+schema+`}}]}`
		w := accountTestCall(app, "POST", "/v1/chat/completions", "client", body)
		if w.Code != 200 || calls != 1 {
			t.Fatal(channel, w.Code, w.Body.String())
		}
		if channel == "playground" {
			recursive := strings.Replace(body, `"type":"string","enum":["fixed"]`, `"$ref":"#/$defs/answer"`, 1)
			w = accountTestCall(app, "POST", "/v1/chat/completions", "client", recursive)
			if w.Code != 400 || calls != 1 {
				t.Fatal("recursive schema weakened or sent", w.Code, calls)
			}
		}
	}
}
