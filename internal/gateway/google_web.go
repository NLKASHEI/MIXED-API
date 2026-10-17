package gateway

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/NLKASHEI/MIXED-API/internal/googleweb"
)

// SetGoogleSessions is called once, before serving, by the platform host.
func (s *Server) SetGoogleSessions(sessions googleweb.Sessions) { s.google = sessions }

func (s *Server) serveGoogleWeb(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) {
	if !isBuiltinURL(b) {
		jsonError(w, 400, "来源内置端点无效")
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		jsonError(w, 501, "该来源的此接口转换尚未完成")
		return
	}
	kind := b.ID
	if kind == "aistudio" {
		var ok bool
		kind, model, ok = strings.Cut(model, "/")
		if !ok || (kind != "build" && kind != "playground") {
			jsonError(w, 400, "AIS 模型需要 build/ 或 playground/ 能力前缀")
			return
		}
	}
	if model == "" || strings.ContainsAny(model, "/\\?#%") {
		jsonError(w, 400, "模型 ID 无效")
		return
	}
	body, err := expressPayload(payload, model)
	if err != nil {
		jsonError(w, 400, err.Error())
		return
	}
	if s.google == nil {
		jsonError(w, 503, "来源会话不可用，请在应用内完成授权步骤")
		return
	}
	settings := s.sourceSettings(b.ID)
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(settings.RequestTimeoutSeconds)*time.Second)
	defer cancel()
	raw, _ := json.Marshal(body)
	op := googleweb.Operation{Kind: kind, Model: model, Body: raw}
	var stream bool
	json.Unmarshal(payload["stream"], &stream)
	if kind == "build" {
		s.mu.RLock()
		unary, known := s.googleBuildUnary[model]
		s.mu.RUnlock()
		// Unknown models use the general unary method. Catalogued free models
		// use the incremental RPC; subscription models require unary transport.
		op.Unary = !stream || !known || unary
		if op.Unary {
			kind = "build_unary"
		}
	}
	if kind == "playground" {
		op, err = playgroundOperation(model, body)
		if err != nil {
			jsonError(w, 400, err.Error())
			return
		}
	}
	resp, err := s.sendGoogle(ctx, b.ID, op, settings.MaxRetries)
	if err != nil {
		jsonError(w, 503, "来源会话未就绪或已失效，请打开配置中的授权窗口")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		providerFailure(w, resp)
		return
	}
	w.Header().Set("X-Mixed-Backend", b.ID)
	if !stream || settings.AggregateStream {
		expected := 1
		if raw, ok := payload["n"]; ok {
			json.Unmarshal(raw, &expected)
		}
		var result geminiResult
		outputBytes := 0
		candidates := map[int]int{}
		err = decodeGoogleWeb(ctx, kind, resp.Body, func(frame geminiResult) error {
			raw, _ := json.Marshal(frame)
			outputBytes += len(raw)
			if outputBytes > maxBody {
				return errors.New("generation output too large")
			}
			for _, c := range frame.Candidates {
				if c.Index < 0 || c.Index >= expected {
					return errors.New("invalid candidate index")
				}
				index, ok := candidates[c.Index]
				if !ok {
					index = len(result.Candidates)
					candidates[c.Index] = index
					result.Candidates = append(result.Candidates, c)
				} else {
					dst := &result.Candidates[index]
					dst.Content.Parts = append(dst.Content.Parts, c.Content.Parts...)
					if c.Finish != "" {
						dst.Finish = c.Finish
					}
				}
			}
			if frame.Usage.Total > 0 {
				result.Usage = frame.Usage
			}
			return nil
		})
		if err != nil || len(result.Candidates) != expected {
			jsonError(w, 502, "服务方生成未完成，请检查授权、模型权限与网络")
			return
		}
		choices := []any{}
		for _, c := range result.Candidates {
			if c.Finish == "" {
				jsonError(w, 502, "服务方响应在完成前中断")
				return
			}
			choices = append(choices, map[string]any{"index": c.Index, "message": geminiMessage(c.Content.Parts, false, 0), "finish_reason": geminiFinish(c.Finish, hasGeminiCalls(c.Content.Parts))})
		}
		if stream {
			out := newSSEOutput(w, publicModel)
			chunks := []any{}
			for _, c := range result.Candidates {
				chunks = append(chunks, map[string]any{"index": c.Index, "delta": geminiMessage(c.Content.Parts, true, 0), "finish_reason": geminiFinish(c.Finish, hasGeminiCalls(c.Content.Parts))})
			}
			if out.send(map[string]any{"id": out.id, "object": "chat.completion.chunk", "created": out.created, "model": publicModel, "choices": chunks}) != nil {
				return
			}
			var options struct {
				Include bool `json:"include_usage"`
			}
			json.Unmarshal(payload["stream_options"], &options)
			if options.Include {
				if out.chunk(nil, nil, geminiUsage(result)) != nil {
					return
				}
			}
			out.done()
			return
		}
		jsonResult(w, 200, map[string]any{"id": "chatcmpl-" + newKey(""), "object": "chat.completion", "created": time.Now().Unix(), "model": publicModel, "choices": choices, "usage": geminiUsage(result)})
		return
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	// The pipe gives the browser decoder the same backpressure and cancellation
	// as the shared Gemini converter. A terminal frame is withheld until the
	// complete RPC trailer has been checked.
	go func() {
		terminal := geminiResult{}
		finishes := map[int]string{}
		err := decodeGoogleWeb(ctx, kind, resp.Body, func(frame geminiResult) error {
			for i := range frame.Candidates {
				c := &frame.Candidates[i]
				if c.Finish != "" {
					finishes[c.Index] = c.Finish
					c.Finish = ""
				}
			}
			if frame.Usage.Total > 0 {
				terminal.Usage = frame.Usage
			}
			data, _ := json.Marshal(frame)
			_, err := fmt.Fprintf(writer, "data: %s\n\n", data)
			return err
		})
		if err == nil && len(finishes) > 0 {
			for index, finish := range finishes {
				c := geminiResult{}.Candidates
				c = append(c, struct {
					Index   int           `json:"index"`
					Content geminiContent `json:"content"`
					Finish  string        `json:"finishReason"`
				}{Index: index, Finish: finish})
				terminal.Candidates = append(terminal.Candidates, c...)
			}
			data, _ := json.Marshal(terminal)
			_, err = fmt.Fprintf(writer, "data: %s\n\n", data)
		}
		writer.CloseWithError(err)
	}()
	s.streamExpress(w, r, reader, "chatcmpl-"+newKey(""), publicModel, time.Now().Unix(), payload)
}

// Retry only an explicit pre-generation busy/rate-limit response, never a
// lost connection or partially delivered generation. Respect Retry-After.
func (s *Server) sendGoogle(ctx context.Context, id string, op googleweb.Operation, retries int) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := s.google.Send(ctx, id, op)
		if err != nil || attempt >= retries || resp.StatusCode != 429 && resp.StatusCode != 503 {
			return resp, err
		}
		delay := time.Duration(1<<min(attempt, 5)) * time.Second
		if raw := resp.Header.Get("Retry-After"); raw != "" {
			if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
				// Larger values outlive any allowed request deadline. Clamp before
				// multiplication so malformed values cannot overflow into a retry.
				delay = time.Duration(min(seconds, 86400)) * time.Second
			} else if deadline, err := http.ParseTime(raw); err == nil {
				delay = max(0, time.Until(deadline))
			}
		}
		resp.Body.Close()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Each protocol emits Gemini frames into one shared conversion path. Sparse
// JSON arrays are accepted only for the Google JSON+protobuf transport.
func decodeGoogleWeb(ctx context.Context, kind string, source io.Reader, emit func(geminiResult) error) error {
	emitJSON := func(raw []byte) error {
		var frame geminiResult
		if json.Unmarshal(raw, &frame) != nil || (len(frame.Error) > 0 && string(frame.Error) != "null") {
			return errors.New("invalid generation frame")
		}
		for i := range frame.Candidates {
			if frame.Candidates[i].Finish == "FINISH_REASON_UNSPECIFIED" {
				frame.Candidates[i].Finish = ""
			}
		}
		return emit(frame)
	}
	if kind == "aps" {
		return scanGoogleObjects(ctx, source, func(raw []byte) error {
			var obj struct {
				Results []struct {
					Errors []json.RawMessage `json:"errors"`
					Data   struct {
						UI struct {
							Result json.RawMessage `json:"streamGenerateContentAnonymous"`
						} `json:"ui"`
					} `json:"data"`
				} `json:"results"`
			}
			if json.Unmarshal(raw, &obj) != nil {
				return errors.New("invalid GraphQL frame")
			}
			for _, result := range obj.Results {
				if len(result.Errors) > 0 {
					return errors.New("GraphQL generation failed")
				}
				raw := result.Data.UI.Result
				if len(raw) == 0 {
					continue
				}
				if raw[0] == '[' {
					var frames []json.RawMessage
					if json.Unmarshal(raw, &frames) != nil {
						return errors.New("invalid GraphQL list")
					}
					for _, f := range frames {
						if err := emitJSON(f); err != nil {
							return err
						}
					}
				} else {
					if err := emitJSON(raw); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}
	dec := json.NewDecoder(&sparseArrayReader{reader: bufio.NewReader(io.LimitReader(source, 128<<20))})
	if kind == "build_unary" {
		var frame []json.RawMessage
		if dec.Decode(&frame) != nil {
			return errors.New("invalid unary RPC response")
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			return errors.New("extra unary RPC content")
		}
		data, err := googleProxyBody(frame)
		if err != nil {
			return err
		}
		return emitJSON(data)
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('[') {
		return errors.New("invalid RPC root")
	}
	if token, err := dec.Token(); err != nil || token != json.Delim('[') {
		return errors.New("invalid RPC frames")
	}
	for dec.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var frame []json.RawMessage
		if dec.Decode(&frame) != nil {
			return errors.New("invalid RPC frame")
		}
		if kind == "playground" {
			value, err := decodePlaygroundFrame(frame)
			if err != nil {
				return err
			}
			if err = emit(value); err != nil {
				return err
			}
			continue
		}
		data, err := googleProxyBody(frame)
		if err != nil {
			return err
		}
		if err := emitJSON(data); err != nil {
			return err
		}
	}
	if token, err := dec.Token(); err != nil || token != json.Delim(']') {
		return errors.New("truncated RPC frames")
	}
	for dec.More() {
		var trailer []any
		if dec.Decode(&trailer) != nil {
			return errors.New("invalid RPC trailer")
		}
		if len(trailer) > 0 && trailer[0] != nil && trailer[0] != float64(0) {
			return errors.New("RPC rejected generation")
		}
	}
	if token, err := dec.Token(); err != nil || token != json.Delim(']') {
		return errors.New("truncated RPC root")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("extra RPC content")
	}
	return nil
}

func googleProxyBody(frame []json.RawMessage) ([]byte, error) {
	var data string
	if len(frame) > 0 && string(frame[0]) != "null" && json.Unmarshal(frame[0], &data) == nil {
		return []byte(data), nil
	}
	if len(frame) > 2 && json.Unmarshal(frame[2], &data) == nil {
		decoded, err := base64.StdEncoding.DecodeString(data)
		if err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("empty or invalid RPC body")
}

// Streaming brace scanner bounds each GraphQL object and handles arbitrary
// network fragmentation without retaining the complete response.
func scanGoogleObjects(ctx context.Context, r io.Reader, emit func([]byte) error) error {
	reader := bufio.NewReader(r)
	depth := 0
	array, value, seen := false, false, false
	quoted, escaped := false, false
	var data []byte
	for {
		c, err := reader.ReadByte()
		if err != nil {
			if err == io.EOF && depth == 0 && !array && seen {
				return nil
			}
			if err == io.EOF {
				return errors.New("truncated GraphQL response")
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if depth == 0 {
			switch c {
			case ' ', '\t', '\r', '\n':
				continue
			case '[':
				if array {
					return errors.New("invalid GraphQL root")
				}
				array = true
				value = false
				continue
			case ']':
				if !array || !value {
					return errors.New("invalid GraphQL root")
				}
				array = false
				continue
			case ',':
				if !array || !value {
					return errors.New("invalid GraphQL separator")
				}
				value = false
				continue
			case '{':
				if array && value {
					return errors.New("missing GraphQL separator")
				}
				value = true
				seen = true
			default:
				return errors.New("invalid GraphQL content")
			}
			data = data[:0]
		}
		data = append(data, c)
		if len(data) > maxBody {
			return errors.New("GraphQL frame too large")
		}
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				if err := emit(data); err != nil {
					return err
				}
			}
		}
	}
}

type sparseArrayReader struct {
	reader          *bufio.Reader
	pending         []byte
	quoted, escaped bool
	previous        byte
}

func (r *sparseArrayReader) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(r.pending) > 0 {
			used := copy(p[n:], r.pending)
			n += used
			r.pending = r.pending[used:]
			continue
		}
		if n > 0 && r.reader.Buffered() == 0 {
			return n, nil
		}
		c, err := r.reader.ReadByte()
		if err != nil {
			if n > 0 {
				return n, nil
			}
			return 0, err
		}
		r.pending = []byte{c}
		if r.quoted {
			if r.escaped {
				r.escaped = false
			} else if c == '\\' {
				r.escaped = true
			} else if c == '"' {
				r.quoted = false
			}
			continue
		}
		if (c == ',' && (r.previous == '[' || r.previous == ',')) || (c == ']' && r.previous == ',') {
			r.pending = append([]byte("null"), c)
		}
		if c == '"' {
			r.quoted = true
		}
		if c != ' ' && c != '\r' && c != '\n' && c != '\t' {
			r.previous = c
		}
	}
	return n, nil
}
