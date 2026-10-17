package gateway

import (
	_ "embed"
	"encoding/json"
)

type upstreamSource struct {
	ID       string `json:"id"`
	Repo     string `json:"repo"`
	Revision string `json:"revision"`
	Version  string `json:"version,omitempty"`
	URL      string `json:"url,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
	Entry    string `json:"entry,omitempty"`
	NPM      bool   `json:"npm,omitempty"`
	License  string `json:"license"`
	Note     string `json:"note"`
}
type sourceCatalog struct {
	CheckedOn string           `json:"checked_on"`
	Sources   []upstreamSource `json:"sources"`
}

//go:embed upstreams.json
var upstreamLock []byte

var upstreamCatalog = func() sourceCatalog {
	var catalog sourceCatalog
	if err := json.Unmarshal(upstreamLock, &catalog); err != nil {
		panic(err)
	}
	return catalog
}()
