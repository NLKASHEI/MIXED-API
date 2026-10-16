# ADR-0001: Use protocol delegation instead of bundling six upstreams

Status: Accepted — 2026-10-15

Historical implementation: v0.3.1. ADR-0003 supersedes the external-component delivery decision for future development; it has not yet been implemented. Routing and credential isolation remain in force.

Context: Six provider proxies span Go, Python and Node.js; they require desktop apps, browser login, private Google endpoints, or SillyTavern server integration. They also have incompatible redistribution licenses.

Decision: MIXED-API ships only its own native proxy and simple management panel; backend processes are optional, separately installed by users. Each backend owns authentication, protocol conversion and its model list. The gateway validates local credentials, discovers namespaced models, removes the namespace from outbound requests, then streams the corresponding upstream HTTP response. The client chooses `backend/model`; the gateway never substitutes another provider.

Consequences: No all-in-one runtime for every platform. Android can route locally accessible proxies or authorized remote ones but does not run Codex CLI, WorkBuddy or Camoufox internally. Streaming headers and payload pass through; mismatched upstream endpoint support remains mismatched. Desktop and Android implementations must undergo behavioral parity tests. No user credentials in CI, repository or release files.
