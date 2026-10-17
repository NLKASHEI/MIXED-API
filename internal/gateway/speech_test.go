package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpeechRejectsMalformedWaveAndStereoMetadata(t *testing.T) {
	pcm := []byte{0, 0, 1, 0}
	wave := speechWave(pcm, 24000)
	malformed := append(append([]byte{}, wave...), []byte{1, 2, 3, 4}...)
	binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
	for _, test := range []struct {
		mime string
		data []byte
	}{{"audio/wav", malformed}, {"audio/L16;codec=pcm;rate=24000;channels=2", pcm}} {
		cfg := Defaults()
		cfg.APIKey = "client"
		cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
		app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
		app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			chunk := `{"candidates":[{"index":0,"content":{"parts":[{"inlineData":{"mimeType":"` + test.mime + `","data":"` + base64.StdEncoding.EncodeToString(test.data) + `"}}]},"finishReason":"STOP"}]}`
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(chunk))}, nil
		})
		w := accountTestCall(app, "POST", "/v1/audio/speech", "client", `{"model":"vertex/tts","input":"hi","voice":"Kore"}`)
		if w.Code != 502 {
			t.Errorf("invalid audio succeeded: %s status %d", test.mime, w.Code)
		}
	}
}

func TestGoogleSpeechReturnsPlayableWaveAcrossCapabilities(t *testing.T) {
	for _, tc := range []struct{ id, model string }{{"vertex", "gemini-tts"}, {"aps", "gemini-tts"}, {"aistudio", "build/gemini-tts"}, {"aistudio", "playground/gemini-tts"}} {
		t.Run(tc.id+tc.model, func(t *testing.T) {
			cfg := Defaults()
			cfg.APIKey = "client"
			cfg.Backends = []Backend{{ID: tc.id, Kind: "builtin", URL: builtinBase(tc.id), ProviderKey: "fixture-key", Enabled: true}}
			app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
			chunk := `{"candidates":[{"index":0,"content":{"parts":[{"inlineData":{"mimeType":"audio/L16;codec=pcm;rate=24000","data":"AAABAA=="}}]},"finishReason":"STOP"}]}`
			validate := func(raw []byte) {
				if !bytes.Contains(raw, []byte("Kore")) {
					t.Error("voice not mapped")
				}
			}
			app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(r.Body)
				validate(raw)
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(chunk))}, nil
			})
			app.SetGoogleSessions(&fixtureGoogleBrowser{send: func(ctx context.Context, id string, op googleweb.Operation) (*http.Response, error) {
				validate(op.Body)
				data := `[{"results":[{"data":{"ui":{"streamGenerateContentAnonymous":` + chunk + `}}}]}]`
				if op.Kind == "build" {
					raw, _ := json.Marshal(chunk)
					data = `[` + string(raw) + `]`
				}
				if op.Kind == "playground" {
					data = `[[[[[[[[null,null,["audio/L16;codec=pcm;rate=24000","AAABAA=="]]],"model"],1]],null,[2,1,3]]]]`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(data))}, nil
			}})
			raw, _ := json.Marshal(map[string]any{"model": tc.id + "/" + tc.model, "input": "Hello", "voice": "Kore", "response_format": "wav"})
			w := accountTestCall(app, "POST", "/v1/audio/speech", "client", string(raw))
			if w.Code != 200 {
				t.Fatalf("speech: %d %s", w.Code, w.Body.String())
			}
			audio := w.Body.Bytes()
			if len(audio) != 48 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" || binary.LittleEndian.Uint32(audio[24:28]) != 24000 || !bytes.Equal(audio[44:], []byte{0, 0, 1, 0}) {
				t.Fatal("invalid audio container")
			}
		})
	}
}

func TestGoogleReceivesAudioAndPDFInput(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "fixture-key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		for _, want := range []string{"audio/wav", "application/pdf", "MEDIA_RESOLUTION_HIGH"} {
			if !strings.Contains(string(raw), want) {
				t.Errorf("missing %s", want)
			}
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"index":0,"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))}, nil
	})
	w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/gemini-test","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"format":"wav","data":"AAABAA=="}},{"type":"file","file":{"file_data":"data:application/pdf;base64,JVBERg=="}}]}],"media_resolution":"high"}`)
	if w.Code != 200 || calls != 1 {
		t.Fatalf("media request: %d %s", w.Code, w.Body.String())
	}
}

func TestGoogleAudioPreservesExplicitModalities(t *testing.T) {
	cfg := Defaults()
	cfg.APIKey = "client"
	cfg.Backends = []Backend{{ID: "vertex", Kind: "builtin", URL: builtinBase("vertex"), ProviderKey: "key", Enabled: true}}
	app := NewServer(filepath.Join(t.TempDir(), "config.json"), cfg)
	calls := 0
	app.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		var input map[string]any
		json.NewDecoder(r.Body).Decode(&input)
		modes := input["generationConfig"].(map[string]any)["responseModalities"].([]any)
		if len(modes) != 2 || modes[0] != "TEXT" || modes[1] != "AUDIO" {
			t.Error("explicit text/audio modalities lost")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"candidates":[{"index":0,"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`))}, nil
	})
	for _, test := range []struct {
		modes  string
		status int
	}{{`["text","audio"]`, 200}, {`["text"]`, 400}} {
		w := accountTestCall(app, "POST", "/v1/chat/completions", "client", `{"model":"vertex/tts","messages":[{"role":"user","content":"hi"}],"modalities":`+test.modes+`,"audio":{"voice":"Kore","format":"pcm16"}}`)
		if w.Code != test.status {
			t.Errorf("modalities %s: %d", test.modes, w.Code)
		}
	}
	if calls != 1 {
		t.Errorf("invalid modalities reached provider: %d calls", calls)
	}
}
