# agent-dlocal

A dLocal investigation and triage CLI built for AI agents.

dLocal is an emerging-markets payment processor (LatAm, Africa, Asia; payins and payouts).
`agent-dlocal` turns "why did this payment fail?", "where did this payout go?", and "what happened
to this refund?" into single commands that emit compact, structured, redacted output — so an LLM can
answer them without ever seeing a credential.

## Status

Early development. The command surface is being built out against the endpoint inventory in
`design-docs/initial-design.md`.

## Install

```
brew install shhac/tap/agent-dlocal
```

## Quick start

```
agent-dlocal auth add prod --form
agent-dlocal auth check prod
```

`--form` prompts for the X-Login, X-Trans-Key, and Secret key in a single native OS dialog, so the
secrets go straight from the user into the OS keychain — they are never typed into a chat transcript
and never pass through the model's context.

## Output contract

- Lists stream NDJSON by default; `--format json|yaml` are available.
- `get <id>...` accepts multiple ids and returns one record per id in input order. A miss emits an
  `@unresolved` line on stdout with exit 0; only command-level failures go to stderr with exit 1.
- Sensitive fields are redacted by default. `--expose <path,key>` opts out per invocation. Stored
  credentials are never exposable.
- Errors are JSON on stderr: `{"error", "fixable_by": "agent"|"human"|"retry", "hint"}`.

## License

PolyForm Perimeter 1.0.0 — see `LICENSE`.
