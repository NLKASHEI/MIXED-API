package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const resumeMemoryLimit = 64 << 20
const resumeReplyLimit = 8 << 20

type resumeEvent struct {
	Seq  int    `json:"seq"`
	Data string `json:"data"`
}
type resumeEntry struct {
	id, key, owner, model string
	fingerprint           string
	readers               int
	events                []resumeEvent
	header                http.Header
	status                int
	done                  bool
	result                string
	bytes                 int
	finished              time.Time
	ttl                   time.Duration
	changed               chan struct{}
	cancel                context.CancelFunc
}
type resumeStore struct {
	mu      sync.Mutex
	entries map[string]*resumeEntry
	keys    map[string]string
	bytes   int
	closed  bool
}

func (s *resumeStore) signal(e *resumeEntry) { close(e.changed); e.changed = make(chan struct{}) }
func resumeOwner(key string) string          { h := sha256.Sum256([]byte(key)); return hex.EncodeToString(h[:]) }
func (s *resumeStore) prune() {
	for id, e := range s.entries {
		if e.done && e.readers == 0 && time.Since(e.finished) > e.ttl {
			s.bytes -= e.bytes
			delete(s.entries, id)
			if s.keys[e.key] == id {
				delete(s.keys, e.key)
			}
		}
	}
}
func (s *resumeStore) append(e *resumeEntry, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.done || s.closed {
		return context.Canceled
	}
	if e.bytes+len(data) > resumeReplyLimit || s.bytes+len(data) > resumeMemoryLimit || len(e.events) >= 32768 {
		return fmt.Errorf("resume buffer full")
	}
	e.events = append(e.events, resumeEvent{Seq: len(e.events), Data: data})
	e.bytes += len(data)
	s.bytes += len(data)
	s.signal(e)
	return nil
}
func (s *Server) CloseResumable() {
	s.resumes.mu.Lock()
	defer s.resumes.mu.Unlock()
	s.resumes.closed = true
	for _, e := range s.resumes.entries {
		e.cancel()
		if !e.done {
			e.done = true
			e.result = "cancelled"
			e.finished = time.Now()
			s.resumes.signal(e)
		}
	}
}

type resumePumpKey struct{}
type resumeOwnerKey struct{}

func (s *Server) resumableExpress(w http.ResponseWriter, r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage) bool {
	if r.Context().Value(resumePumpKey{}) != nil || r.URL.Path != "/v1/chat/completions" {
		return false
	}
	settings := s.sourceSettings("vertex")
	var stream bool
	json.Unmarshal(payload["stream"], &stream)
	if !settings.ResumableEnabled || !stream {
		return false
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 256 {
		jsonError(w, 400, "Idempotency-Key 不能超过256字节")
		return true
	}
	from, err := resumeCursor(r)
	if err != nil {
		jsonError(w, 400, "续传游标无效")
		return true
	}
	owner, _ := r.Context().Value(resumeOwnerKey{}).(string)
	if owner == "" {
		owner = resumeOwner(s.config().APIKey)
	}
	raw, _ := json.Marshal(payload)
	fingerprint := resumeOwner(owner + "\x00" + key + "\x00" + publicModel + "\x00" + b.ProviderKey + "\x00" + b.ExpressProject + "\x00" + b.ExpressLocation + "\x00" + string(raw))
	store := &s.resumes
	store.mu.Lock()
	store.prune()
	if store.closed {
		store.mu.Unlock()
		jsonError(w, 503, "服务正在关闭")
		return true
	}
	if store.entries == nil {
		store.entries = map[string]*resumeEntry{}
		store.keys = map[string]string{}
	}
	lookup := resumeOwner(owner + "\x00" + key)
	entry := store.entries[store.keys[lookup]]
	if key != "" && entry != nil && entry.fingerprint != fingerprint {
		store.mu.Unlock()
		jsonError(w, 409, "Idempotency-Key already belongs to a different request")
		return true
	}
	if key == "" {
		entry = nil
	}
	if entry == nil {
		if from > 0 {
			store.mu.Unlock()
			jsonError(w, 410, "原回复缓冲已不存在，请发起新的请求")
			return true
		}
		if len(store.entries) >= 32 || store.bytes >= resumeMemoryLimit {
			store.mu.Unlock()
			jsonError(w, 503, "续传缓冲已满，请完成或取消现有请求")
			return true
		}
		ttl := settings.ResumableTTLSeconds
		if ttl == 0 {
			ttl = 300
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(settings.RequestTimeoutSeconds)*time.Second)
		entry = &resumeEntry{id: newKey("rsm-"), key: lookup, fingerprint: fingerprint, owner: owner, model: publicModel, result: "running", header: make(http.Header), changed: make(chan struct{}), cancel: cancel, ttl: time.Duration(ttl) * time.Second}
		store.entries[entry.id] = entry
		if key != "" {
			store.keys[lookup] = entry.id
		}
		producer := r.Clone(context.WithValue(context.WithValue(ctx, resumePumpKey{}, true), diagnosticKey{}, &requestDiagnostic{}))
		go s.produceResumable(producer, b, publicModel, model, payload, entry)
	}
	entry.readers++
	store.mu.Unlock()
	s.readResumable(w, r, entry, from)
	return true
}

func resumeCursor(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("resume_from")
	if raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 {
			return 0, fmt.Errorf("invalid cursor")
		}
		return n, nil
	}
	if raw = r.Header.Get("Last-Event-ID"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 0 || n >= int(^uint(0)>>1) {
			return 0, fmt.Errorf("invalid event ID")
		}
		return n + 1, nil
	}
	return 0, nil
}

type resumeWriter struct {
	store   *resumeStore
	entry   *resumeEntry
	header  http.Header
	status  int
	pending []byte
	failure error
}

func (w *resumeWriter) Header() http.Header { return w.header }
func (w *resumeWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *resumeWriter) Flush() {}
func (w *resumeWriter) Write(raw []byte) (int, error) {
	if w.failure != nil {
		return 0, w.failure
	}
	if w.status == 0 {
		w.status = 200
	}
	if len(w.pending)+len(raw) > resumeReplyLimit {
		w.failure = fmt.Errorf("resume event limit")
		return 0, w.failure
	}
	w.pending = append(w.pending, raw...)
	if !strings.Contains(w.header.Get("Content-Type"), "text/event-stream") {
		return len(raw), nil
	}
	for {
		n := bytes.Index(w.pending, []byte("\n\n"))
		if n < 0 {
			break
		}
		frame := string(w.pending[:n+2])
		w.pending = w.pending[n+2:]
		w.store.mu.Lock()
		if w.entry.status == 0 {
			w.entry.status = w.status
			w.entry.header = w.header.Clone()
		}
		w.store.mu.Unlock()
		if err := w.store.append(w.entry, frame); err != nil {
			w.failure = err
			w.entry.cancel()
			return 0, err
		}
	}
	return len(raw), nil
}
func (s *Server) produceResumable(r *http.Request, b Backend, publicModel, model string, payload map[string]json.RawMessage, e *resumeEntry) {
	defer e.cancel()
	writer := &resumeWriter{store: &s.resumes, entry: e, header: make(http.Header)}
	started := time.Now()
	s.serveExpress(writer, r, b, publicModel, model, payload)
	defer func() {
		d := diagnostic(r)
		d.mu.Lock()
		details := d.details
		d.mu.Unlock()
		s.resumes.mu.Lock()
		result := e.result
		originalStatus := e.status
		s.resumes.mu.Unlock()
		status := 200
		failure := ""
		if result != "done" {
			status = 502
			if originalStatus >= 400 {
				status = originalStatus
			}
			failure = "续传缓冲中的来源生成未完成"
		}
		s.addLog(RequestLog{RequestID: e.id, Model: publicModel, Backend: "vertex", Method: "POST", Path: "/v1/chat/completions", Time: started.Format(time.RFC3339), Status: status, DurationMs: time.Since(started).Milliseconds(), Error: failure, Details: &details})
	}()
	s.resumes.mu.Lock()
	if e.status == 0 {
		e.status = writer.status
		e.header = writer.header.Clone()
	}
	s.resumes.mu.Unlock()
	if len(writer.pending) > 0 && writer.failure == nil {
		data := string(writer.pending)
		if !strings.Contains(writer.header.Get("Content-Type"), "text/event-stream") {
			data = "data: " + strings.TrimSpace(data) + "\n\n"
		}
		writer.failure = s.resumes.append(e, data)
	}
	store := &s.resumes
	store.mu.Lock()
	defer store.mu.Unlock()
	if e.done {
		return
	}
	if e.status == 0 {
		e.status = writer.status
		if e.status == 0 {
			e.status = 502
		}
		e.header = writer.header.Clone()
	}
	e.done = true
	e.finished = time.Now()
	e.result = "error"
	if len(e.events) > 0 && strings.TrimSpace(e.events[len(e.events)-1].Data) == "data: [DONE]" && writer.failure == nil && r.Context().Err() == nil {
		e.result = "done"
	}
	if r.Context().Err() != nil {
		e.result = "cancelled"
	}
	store.signal(e)
}
func (s *Server) releaseResumeReader(e *resumeEntry) {
	s.resumes.mu.Lock()
	e.readers--
	s.resumes.mu.Unlock()
}
func (s *Server) readResumable(w http.ResponseWriter, r *http.Request, e *resumeEntry, from int) {
	defer s.releaseResumeReader(e)
	controller := http.NewResponseController(w)
	store := &s.resumes
	started := false
	for {
		store.mu.Lock()
		count := len(e.events)
		if from > count {
			store.mu.Unlock()
			if !started {
				jsonError(w, 400, "续传游标超过已收到的事件")
			}
			return
		}
		events := append([]resumeEvent(nil), e.events[from:]...)
		done, result, status := e.done, e.result, e.status
		header := e.header.Clone()
		changed := e.changed
		store.mu.Unlock()
		if !started && (len(events) > 0 || done) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Mixed-Stream-ID", e.id)
			w.Header().Set("X-Mixed-Backend", "vertex")
			if retry := header.Get("Retry-After"); retry != "" {
				w.Header().Set("Retry-After", retry)
			}
			if status < 100 {
				status = 502
			}
			w.WriteHeader(status)
			started = true
		}
		for _, event := range events {
			controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			if _, err := fmt.Fprintf(w, "id: %d\n%s", event.Seq, event.Data); err != nil {
				return
			}
			from = event.Seq + 1
			if err := controller.Flush(); err != nil {
				return
			}
			controller.SetWriteDeadline(time.Time{})
		}
		if done {
			if result != "done" && status < 400 {
				io.WriteString(w, "data: {\"error\":{\"message\":\"来源响应未完成或已取消；可读取已有分片，不能当作完整回复\"}}\n\n")
			}
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}
func (s *Server) handleResumable(w http.ResponseWriter, r *http.Request, cfg Config) {
	id := strings.TrimPrefix(r.URL.Path, "/v1/resumable/")
	store := &s.resumes
	store.mu.Lock()
	store.prune()
	e := store.entries[id]
	if e == nil || e.owner != resumeOwner(cfg.APIKey) {
		store.mu.Unlock()
		jsonError(w, 404, "续传回复不存在或已过期")
		return
	}
	if r.Method == "DELETE" {
		e.cancel()
		if !e.done {
			e.done = true
			e.result = "cancelled"
			e.finished = time.Now()
			store.signal(e)
		}
		store.mu.Unlock()
		jsonResult(w, 200, map[string]bool{"cancelled": true})
		return
	}
	if r.Method != "GET" {
		store.mu.Unlock()
		jsonError(w, 405, "Use GET or DELETE")
		return
	}
	e.readers++
	store.mu.Unlock()
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		from, err := resumeCursor(r)
		if err != nil {
			s.releaseResumeReader(e)
			jsonError(w, 400, "Invalid resume cursor")
			return
		}
		s.readResumable(w, r, e, from)
		return
	}
	defer s.releaseResumeReader(e)
	from, err := strconv.Atoi(r.URL.Query().Get("from"))
	if r.URL.Query().Get("from") == "" {
		from = 0
		err = nil
	}
	if err != nil || from < 0 {
		jsonError(w, 400, "from无效")
		return
	}
	limit := 128
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	if err != nil || from < 0 || limit < 1 || limit > 512 {
		jsonError(w, 400, "from或limit无效")
		return
	}
	wait := 0
	if raw := r.URL.Query().Get("wait"); raw != "" {
		wait, err = strconv.Atoi(raw)
	}
	if err != nil || wait < 0 || wait > 25 {
		jsonError(w, 400, "wait需为0至25秒")
		return
	}
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	for {
		store.mu.Lock()
		if from > len(e.events) {
			store.mu.Unlock()
			jsonError(w, 400, "续传游标超过已收到的事件")
			return
		}
		if len(e.events) > from || e.done || wait == 0 {
			end := min(len(e.events), from+limit)
			events := append([]resumeEvent{}, e.events[from:end]...)
			done, result := e.done, e.result
			store.mu.Unlock()
			http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Second))
			jsonResult(w, 200, map[string]any{"id": id, "events": events, "next": end, "done": done, "status": result})
			return
		}
		changed := e.changed
		store.mu.Unlock()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			wait = 0
		case <-changed:
		}
	}
}
