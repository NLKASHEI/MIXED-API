package gateway

import (
	"errors"
	"runtime"
)

func (s *Server) backendState(id, value string) {
	s.mu.Lock()
	s.status["setup:"+id] = value
	s.mu.Unlock()
}
func (s *Server) assistStatus() any {
	ids := []string{"vertex", "codex", "opencode", "workbuddy"}
	if s.google != nil {
		ids = append(ids, "aps", "aistudio")
	}
	return map[string]any{"platform": runtime.GOOS, "native_codex_supported": true, "builtin_backends": ids}
}
func (s *Server) enableBackend(id string) error { return s.setBackendEnabled(id, true) }
func (s *Server) setBackendEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.cfg
	next.Backends = append([]Backend(nil), s.cfg.Backends...)
	found := false
	for i := range next.Backends {
		if next.Backends[i].ID == id {
			next.Backends[i].Enabled = enabled
			found = true
			break
		}
	}
	if !found {
		return errors.New("来源不存在")
	}
	if err := Save(s.path, next); err != nil {
		return errors.New("无法保存来源状态")
	}
	s.cfg = next
	return nil
}
