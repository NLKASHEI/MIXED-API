# MIXED-API glossary

- **Gateway**: The standalone MIXED-API HTTP listener, not a model provider.
- **Backend**: An already-running upstream proxy reachable at a configured Base URL.
- **Model namespace**: The stable backend ID prefix such as `codex/`, used to disambiguate names.
- **Adapter**: Routing implementation which strips the namespace and delegates to the backend's existing protocol; does not simulate unsupported endpoints.
- **Client API Key**: Secret granted solely to `/v1/*` routes, not admin configuration.
- **Admin Key**: Independent secret for `/api/*`, including sensitive backend settings and manual process control.
- **Managed process**: Optional *desktop-only* subprocess started after a user configures its command; no provider login is automated.
- **Release**: A CI-produced GitHub prerelease containing separate Windows and Android artifacts.
