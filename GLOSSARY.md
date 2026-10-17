# MIXED-API glossary

These definitions describe the v0.4.0 integrated core. Windows packages and a separately labelled Android preview are published from verified CI builds. The historical v0.3.1 uses external backends. See docs/integration-progress.md for verified capabilities and remaining work.

- **Gateway**: The standalone MIXED-API HTTP listener, not a model provider.
- **Backend**: An optional source, implemented by a native provider module or an explicitly configured external HTTP service.
- **Model namespace**: The stable backend ID prefix such as `codex/`, used to disambiguate names.
- **Adapter**: Routing implementation which strips the namespace and delegates to the backend's existing protocol; does not simulate unsupported endpoints.
- **Client API Key**: Secret granted solely to `/v1/*` routes, not admin configuration.
- **Admin Key**: Independent secret for `/api/*`, including sensitive source settings and account setup.
- **Legacy process settings**: Old command/workdir fields retained for configuration compatibility; the application never executes them.
- **Release**: A CI-produced GitHub release. Platform assets follow `packaging/release-targets.json`; Android can be deferred independently of Windows.

- **快速V**: The user-facing name of the vertex Express adapter; requires the user's Google Express API Key.
- **无头V**: The user-facing name of the aps source; same-family runtime implementations remain internal.
- **Model alias**: A user-defined simple name bound to one explicit backend/model, never an automatic fallback.
- **Preparation**: Selecting built-in configuration without enabling the source or downloading upstream applications.
- **Activation**: Enabling/starting a prepared source only after user confirmation of the authorization step.
- **Integrated core module**: Provider protocol and reverse-proxy logic owned by the application, using shared routing, streaming and credential boundaries; not an installer or launcher for an upstream application.

- **AIS capabilities**: Build and Playground are distinct RPC protocols under the same aistudio source. Models are `aistudio/build/original` or `aistudio/playground/original`.
- **Provider window**: Official website session; only the user completes sign-in, consent and challenges. Windows connects the current default Edge/Chrome profile through the bundled browser extension. Windows automatically initializes after the user completes the official page, without a second connect button or a manual test message; a separate API test verifies inference. It has no admin bridge or client credentials. See ADR-0007/0008.
- **Android PC bridge**: Explicit `pc/original` namespace, including a desktop namespace inside original. No implicit fallback to the PC.
