# agent-dlocal

This repository contains a Go CLI for LLM-driven dLocal payment investigation and triage.

## Development

- Use `make test` for the full test suite.
- Use `make build` to build `agent-dlocal`.
- Use `make build-mock` to build the local mock dLocal server.
- Use `make mock` to start `mockdlocal` on `127.0.0.1:12112`.
- Use `make mock-dev ARGS="payments get D-4-abc"` to run the CLI against the mock server.
- Use `mockdlocal --routes` or `GET /` on the mock server to inspect the supported mock API surface.

## Standing rules

- Prefer `agent-dlocal auth add <profile> --form` when guiding a user through credential setup; do
  not ask the user to paste an X-Login, X-Trans-Key, or Secret key into chat.
- Prefer `agent-dlocal auth update <profile> --form` when guiding a user through rotating a secret.
- Prefer read-only dLocal commands. dLocal refunds and payouts move real money — no mutating command
  ships without a design document that explicitly approves it.
- Do not print, log, or persist raw credentials outside the credential backend. This includes the
  `Authorization` signature header and any `X-Login` / `X-Trans-Key` echo in `--debug` output.
- Keep list outputs compact and NDJSON-friendly.

## Design Intent

See `design-docs/initial-design.md` for the endpoint inventory, command surface, and dLocal-specific
auth decisions.
See `design-docs/mock-dlocal.md` for the local mock server contract.
