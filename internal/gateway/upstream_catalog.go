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

func sourcePackages() map[string]sourcePackage {
	packages := map[string]sourcePackage{}
	for _, p := range upstreamCatalog.Sources {
		if p.URL == "" && p.Entry != "" {
			packages[p.ID] = sourcePackage{p.Repo, p.Revision, p.Entry, p.NPM}
		}
	}
	return packages
}
func binaryPackages() map[string]binaryUpstream {
	packages := map[string]binaryUpstream{}
	for _, p := range upstreamCatalog.Sources {
		if p.URL != "" {
			packages[p.ID] = binaryUpstream{p.URL, p.SHA256, p.Entry}
		}
	}
	return packages
}

func reviewedCodexVersion() string {
	for _, p := range upstreamCatalog.Sources {
		if p.ID == "codex" {
			return p.Version
		}
	}
	panic("missing reviewed Codex version")
}
