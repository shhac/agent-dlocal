# agent-dlocal

This repository contains a Go CLI for LLM-driven dLocal payment investigation and triage. It is the
dLocal member of the `agent-*` family; `agent-stripe` is the reference implementation for the shared
patterns.

## Development

- `make test` — full suite (unit + e2e).
- `make build` — build `agent-dlocal`.
- `make build-mock` — build the local mock dLocal server.
- `make mock` — start `mockdlocal` on `127.0.0.1:12112`.
- `make mock-dev ARGS="payments get D-4-rejected"` — run the CLI against the mock.
- `mockdlocal --routes`, or `GET /` on the running mock, to inspect the mocked surface.
- `make lint` must be clean before anything is considered done.

## Standing rules

- **Prefer `agent-dlocal auth add <profile> --form`** when guiding a user through credential setup,
  and `auth update <profile> --form` when rotating. Never ask a user to paste an X-Login,
  X-Trans-Key, or secret key into chat.
- **Read-only by default.** Every command is a `GET`. No mutating command ships without a design
  document that explicitly approves it — dLocal refunds and payouts move real money in markets where
  reversal is slow or impossible. The `api` group has no `--method` flag on purpose; do not add one.
- **Never print, log, or persist credentials outside the credential backend.** This includes the
  `Authorization` / `Payload-Signature` values and any `X-Login` / `X-Trans-Key` echo in `--debug`
  output. `internal/credential` has no method that returns a printable secret, and it should stay
  that way.
- **Keep list outputs compact and NDJSON-friendly.**
- **Sign what you send.** The client serializes a body once into a `[]byte` that is both signed and
  sent. Do not add a path that marshals separately for signing — key ordering or whitespace drift
  produces intermittent 401s that look random.
- **Do not unify the two signers.** Payins signs `login + date + body` into `Authorization`; payouts
  v2 signs `body` alone into `Payload-Signature`. They hash different messages, not just different
  headers.
- **Do not add credential classification.** dLocal keys carry no live/sandbox marker, so inferring an
  environment from a key would be a fabrication. Environment is host metadata on the profile.

## When adding or changing a command

Update all of these, or the surfaces drift apart:

1. `agent-dlocal usage` — the root command map in `internal/cli/usage.go`.
2. The relevant domain `usage` (`payments usage`, `investigate usage`, …), same file.
3. `skills/agent-dlocal/SKILL.md`.
4. `skills/agent-dlocal/references/commands.md`, and `references/investigation/<scenario>.md` for a
   new investigate scenario.
5. `README.md`, when the change is user-facing.
6. `design-docs/initial-design.md` — especially the endpoint inventory and command surface.
7. `internal/mockdlocal` — a new endpoint needs a fixture, or the e2e test cannot exist.

## Design intent

- `design-docs/initial-design.md` — endpoint inventory, auth and signing model, command surface,
  output contract, status-code mapping.
- `design-docs/mock-dlocal.md` — the mock server's contract.

Both record *why*, including the two places dLocal's real API contradicted the original brief
(payouts v3 uses OAuth2, not `Payload-Signature`; payouts lives on a separate host). Consult the
docs via Context7 (`/websites/dlocal`) before asserting anything new about the API.
