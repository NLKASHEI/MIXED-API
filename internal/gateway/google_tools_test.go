package gateway

import (
	"context"
	"encoding/json"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoogleExplicitToolContract(t *testing.T) {
	for _, source := range []string{"vertex", "playground"} {
		for _, stream := range []bool{false, true} {
			for _, test := range []struct {
				name, choice, part string
				parallel, success  bool
			}{
				{"named", `{"type":"function","function":{"name":"lookup"}}`, "lookup", false, true},
				{"wrong", `{"type":"function","function":{"name":"lookup"}}`, "other", false, false},
				{"required_missing", `"required"`, "", false, false},
				{"none", `"none"`, "", false, true},
				{"none_called", `"none"`, "lookup", false, false},
				{"parallel", `"required"`, "lookup", true, false},
			} {
				t.Run(source+test.name+fmtBool(stream), func(t *testing.T) {
					id, model := "vertex", "gemini-test"
					if source == "playground" {
						id = "aistudio"
						model = "playground/gemini-test"
					}
					cfg := Defaults()
					cfg.APIKey = "client"
					cfg.Backends = []Backend{{ID: id, Kind: "builtin", URL: builtinBase(id), ProviderKey: "fixture", Enabled: true}}
					app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
					parts := []geminiPart{{Text: "result"}}
					if test.part != "" {
						parts = []geminiPart{}
						p := geminiPart{}
						json.Unmarshal([]byte(`{"functionCall":{"name":"`+test.part+`","args":{}}}`), &p)
						parts = append(parts, p)
						if test.parallel {
							parts = append(parts, p)
						}
					}
					result := map[string]any{"candidates": []any{map[string]any{"index": 0, "content": map[string]any{"parts": parts}, "finishReason": "STOP"}}}
					raw, _ := json.Marshal(result)
					app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
						data := string(raw)
						if stream {
							data = "data: " + data + "\n\n"
						}
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
					})
					app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
						var request []any
						json.Unmarshal(op.Body, &request)
						if test.choice == `"none"` && len(protoArray(request[6])) != 0 {
							t.Error("none retained tools")
						}
						if test.name == "named" && !strings.Contains(string(op.Body), "Use one") {
							t.Error("required instruction absent")
						}
						pp := []any{}
						for _, p := range parts {
							wire := make([]any, 12)
							if p.Call != nil {
								wire[10] = []any{p.Call.Name, protoStruct(map[string]any{}), nil}
							} else {
								wire[1] = p.Text
							}
							pp = append(pp, trimProto(wire))
						}
						wire := []any{[]any{[]any{[]any{[]any{[]any{pp, "model"}, 1}}, nil, []any{2, 1, 3}}}}
						data, _ := json.Marshal(wire)
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(data)))}, nil
					}})
					body := `{"model":"` + id + `/` + model + `","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}},{"type":"function","function":{"name":"other","parameters":{"type":"object"}}}],"tool_choice":` + test.choice + `,"parallel_tool_calls":false,"stream":` + fmtBool(stream) + `}`
					w := accountTestCall(app, "POST", "/v1/chat/completions", "client", body)
					if test.success {
						if w.Code != 200 || stream && !strings.Contains(w.Body.String(), "[DONE]") {
							t.Fatalf("%d %s", w.Code, w.Body.String())
						}
					} else if w.Code != 502 && !(stream && w.Code == 200 && strings.Contains(w.Body.String(), `"error"`) && !strings.Contains(w.Body.String(), "[DONE]")) {
						t.Fatalf("contract falsely succeeded %s", w.Body.String())
					}
				})
			}
		}
	}
}
func fmtBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
