package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

type engineSettings struct {
	ResumableEnabled      bool `json:"resumable_enabled,omitempty"`
	ResumableTTLSeconds   int  `json:"resumable_ttl_seconds,omitempty"`
	MaxRetries            int  `json:"max_retries"`
	RequestTimeoutSeconds int  `json:"request_timeout_seconds"`
	AggregateStream       bool `json:"aggregate_stream"`
}

func (e engineSettings) valid() bool {
	return (e.ResumableTTLSeconds == 0 || e.ResumableTTLSeconds >= 30 && e.ResumableTTLSeconds <= 1800) && e.MaxRetries >= 0 && e.MaxRetries <= 10 && e.RequestTimeoutSeconds >= 10 && e.RequestTimeoutSeconds <= 600
}
func (s *Server) sourceSettings(id string) engineSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.cfg.EngineSettings[id]; ok {
		return v
	}
	if id == "vertex" {
		return engineSettings{MaxRetries: 2, RequestTimeoutSeconds: 300}
	}
	return engineSettings{RequestTimeoutSeconds: 300}
}
func (s *Server) handleEngineSettings(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/engine-settings/")
	if id != "aps" && id != "vertex" {
		jsonError(w, 404, "来源没有这些请求设置")
		return
	}
	if r.Method == "GET" {
		jsonResult(w, 200, s.sourceSettings(id))
		return
	}
	if r.Method != "PUT" {
		jsonError(w, 405, "Use GET or PUT")
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	var value engineSettings
	var extra any
	if dec.Decode(&value) != nil || !value.valid() || id != "vertex" && (value.ResumableEnabled || value.ResumableTTLSeconds != 0) || dec.Decode(&extra) != io.EOF {
		jsonError(w, 400, "重试需为 0–10，超时需为 10–600 秒")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.EngineSettings = map[string]engineSettings{}
	for k, v := range s.cfg.EngineSettings {
		next.EngineSettings[k] = v
	}
	next.EngineSettings[id] = value
	if Save(s.path, next) != nil {
		jsonError(w, 500, "无法保存请求设置")
		return
	}
	s.cfg = next
	jsonResult(w, 200, map[string]bool{"saved": true})
}
