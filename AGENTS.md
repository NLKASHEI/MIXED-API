# MIXED-API agent instructions

Keep the codebase small and auditable. Follow principles from https://github.com/mattpocock/skills, particularly TDD, deep module interfaces, and two-axis code review. The referenced skills are an engineering workflow, not a build system or a replacement for actual CI.

## Agent skills

### Issue tracker

Use GitHub Issues on `NLKASHEI/MIXED-API` for user-facing tickets, security or compatibility defects. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context glossary and ADRs. Read `GLOSSARY.md` and `docs/adr/` before changing routing and auth. See `docs/agents/domain.md`.

## Engineering and shipping rules

1. The public seam is the OpenAI-compatible HTTP interface at `127.0.0.1:8765`; write black-box tests at this interface first. No silent cross-backend fallbacks.
2. Credential handling is security-critical: never log provider tokens or dump configuration into error messages. API Key must not authorize admin APIs. Protect against public binding and endpoint confusion.
3. Model IDs use `backend/original`; explicit user-defined aliases may bind to one such target under ADR-0002. Never assume upstream model IDs are unique. Stream backpressure and cancellation should remain correct.
4. All six backends are OPTIONAL and disabled by default. Avoid importing code from their repositories. Do not perform logins or bypass authentication on behalf of users.
5. Implement Windows core in Go (`internal/gateway/`) and Android in pure Java (`android/`). Share the configuration structure and the admin HTML. Keep parity documented; tests must cover every changed public behavior.
6. Before a release: `go test ./...`, `go vet ./...`, check Android Gradle CI, review implementation *against both spec and standards*. CI success does not constitute Android device testing.
7. All release assets must be linked to an actual successful GitHub Actions build. Do not claim binaries are available before verifying Release assets.
