package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Adapted from AIStudio2API sendBuild and DecodeRPCError (MIT, see
// docs/third-party/google-web.md). Only an explicit unsupported unary method
// permits another call to the same Build model. Never inspect successful
// generation bodies or retry authorization/quota errors here.
func buildUnaryUnsupported(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusOK, http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests:
		return false
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	}
	const limit = 64 << 10
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || len(raw) > limit {
		return false
	}
	dec := json.NewDecoder(&sparseArrayReader{reader: bufio.NewReader(bytes.NewReader(raw))})
	var status []json.RawMessage
	if dec.Decode(&status) != nil || len(status) < 2 {
		return false
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return false
	}
	if bytes.HasPrefix(bytes.TrimSpace(status[1]), []byte("[")) {
		// The wrapped form is [frames, status]. A response with generated
		// frames must not be replayed, even if its final status is unsupported.
		first := bytes.TrimSpace(status[0])
		if !bytes.Equal(first, []byte("null")) {
			var frames []json.RawMessage
			if json.Unmarshal(first, &frames) != nil || len(frames) != 0 {
				return false
			}
		}
		if json.Unmarshal(status[1], &status) != nil || len(status) < 2 {
			return false
		}
	}
	var code int
	var message string
	if json.Unmarshal(status[0], &code) != nil || json.Unmarshal(status[1], &message) != nil {
		return false
	}
	message = strings.ToLower(message)
	return code == 12 || resp.StatusCode == http.StatusBadRequest &&
		strings.Contains(message, "support") &&
		(strings.Contains(message, "stream") || strings.Contains(message, "unary"))
}
