// Package googleweb is the narrow platform boundary for Google website
// sessions. Authentication stays in the browser profile; operations cannot
// carry arbitrary URLs, scripts, cookies, or local administration credentials.
package googleweb

import (
	"context"
	"encoding/json"
	"net/http"
)

type Operation struct {
	Kind      string          `json:"kind"` // build, playground, aps, models
	Model     string          `json:"model,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
	Binding   string          `json:"binding,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Unary     bool            `json:"unary,omitempty"`
	PageToken string          `json:"page_token,omitempty"`
}
type State struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}
type Sessions interface {
	Open(context.Context, string) error
	State(context.Context, string) State
	Send(context.Context, string, Operation) (*http.Response, error)
	Close() error
}
