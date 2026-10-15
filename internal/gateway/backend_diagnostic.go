package gateway

import (
	"bytes"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GUI binaries have no inherited console handles. Capture limited stderr
// diagnostics instead of inheriting os.Stdout/os.Stderr, which can be invalid
// under the Windows GUI subsystem. Stdout is discarded: it can contain prompts.
var diagnosticTerms = regexp.MustCompile(`(?i)\b(error|fatal|failed|failure|exception|traceback|denied|unauthorized|timeout|invalid)\b|错误|失败|拒绝|异常|超时`)

type stderrDiagnostics struct {
	mu       sync.Mutex
	current  bytes.Buffer
	provider string
	server   *Server
}

func (w *stderrDiagnostics) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, c := range p {
		if c == '\n' {
			w.flushLine()
		} else if w.current.Len() < 4096 {
			_ = w.current.WriteByte(c)
		}
	}
	return len(p), nil
}
func (w *stderrDiagnostics) flushLine() {
	text := strings.TrimSpace(w.current.String())
	w.current.Reset()
	if text == "" || !diagnosticTerms.MatchString(text) {
		return
	}
	w.server.addLog(RequestLog{
		Time: time.Now().Format(time.RFC3339), Method: "BACKEND", Path: "后台进程错误",
		Backend: w.provider, Status: 0, DurationMs: 0, Error: safeDiagnostic([]byte(text)),
	})
}
func (s *Server) bindBackendOutput(cmd *exec.Cmd, provider string) {
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderrDiagnostics{server: s, provider: provider}
}
