# agent-dlocal

A read-only dLocal investigation and triage CLI built for AI agents.

dLocal is an emerging-markets payment processor (LatAm, Africa, Asia; payins and payouts).
`agent-dlocal` turns *"why did this payment fail?"*, *"where did this payout go?"*, and *"what
happened to this refund?"* into single commands that emit compact, structured, redacted output — so
an LLM can answer them without ever seeing a credential.

It is the dLocal member of the `agent-*` family and shares its contracts with `agent-stripe`.

## Install

```
brew install shhac/tap/agent-dlocal
```

## Setup

```
agent-dlocal auth add prod --form
agent-dlocal auth check
```

`--form` collects the X-Login, X-Trans-Key, and Secret key in a **single native OS dialog**, so the
secrets go straight from the user's keyboard into the OS keychain. They never appear in a chat
transcript and never pass through a model's context.

If you are automating, `--login`/`--trans-key`/`--secret-key` exist — but prefer `--form` for
anything a human is present for.

Sandbox, and optional mutual TLS:

```
agent-dlocal auth add sbox --sandbox --form
agent-dlocal auth add prod --cert ~/.dlocal/client.pem --key ~/.dlocal/client.key --form
```

`--cert`/`--key` take **paths**; the files stay where you put them under your own permissions. Only
the key passphrase is treated as a secret and stored with the rest.

dLocal keys carry no `test`/`live` marker, so live vs sandbox is a host distinction recorded
explicitly on the profile — nothing is guessed from the credential.

## Use

Start from the question, not the endpoint:

```
agent-dlocal investigate payment D-4-8f2a...     # Why did this payment fail?
agent-dlocal investigate order ORDER-10241       # They say they paid; our order says unpaid
agent-dlocal investigate refund REF-4471         # What happened to this refund?
agent-dlocal investigate payout P-2-91bc...      # Where is this payout?
```

Each returns a `verdict`, a `terminal` flag saying whether the state is final, `next_steps`, and the
`evidence` it drew on.

Direct retrieval when you know what you want:

```
agent-dlocal payments get D-4-aaa D-4-bbb        # multiple ids, one record each
agent-dlocal payments status D-4-aaa             # status triple only
agent-dlocal orders get ORDER-10241              # merchant reference -> payment
agent-dlocal refunds get REF-4471
agent-dlocal chargebacks get CHAR42342
agent-dlocal payouts get P-2-91bc
agent-dlocal payment-methods list --country BR
agent-dlocal api get /payments/D-4-aaa           # GET-only escape hatch
```

`agent-dlocal usage` prints the whole map; each group has its own `usage` too.

## Read-only by design

Every command is a `GET`. The raw `api` group has no `--method` flag — the read-only guarantee is
the absence of a code path, not a check that could later be relaxed. dLocal refunds and payouts move
real money in markets where reversal is slow, manual, or impossible.

## Output contract

- Lists stream NDJSON by default; `--format json|yaml` available.
- `get <id>...` returns one record per id **in input order**. A miss emits an `@unresolved` line on
  stdout with exit 0, so one bad id does not lose the batch. Only command-level failures go to
  stderr with exit 1.
- Sensitive fields are redacted by default — including `payer.document`, which is a national ID
  number (CPF, CUIT, DNI). `--expose <path,key>` opts out per invocation. Stored credentials are
  never exposable.
- Errors are JSON on stderr: `{"error", "fixable_by": "agent"|"human"|"retry", "hint"}`.

## Reading dLocal outcomes

dLocal reports a triple — `status`, `status_code`, `status_detail`. The detail carries the reason;
the status only carries the category.

Payins: `PENDING` 100 · `PAID` 200 · `REJECTED` 300 · `CANCELLED` 400 · `EXPIRED` 600

Payouts: `PENDING` 100 · `PAID` 200 · `REJECTED` 300 · `CANCELLED` 400 · **`DELIVERED` 500**

`DELIVERED` is not final and not a failure — the money is in flight at the beneficiary's bank. It is
the status most often misread, so `investigate payout` calls it out explicitly.

A 401 from dLocal is often **clock skew** rather than a bad secret: `X-Date` is part of the signed
message, so a drifted system clock invalidates an otherwise-correct signature. The error hint says
so.

## MCP

```
agent-dlocal mcp
```

Exposes the agent-facing groups as MCP tools. `auth`, `config`, and `usage` are deliberately not
exposed — credential management is an operator task, not something a tool-calling loop should reach.

## Development

```
make build          # ./agent-dlocal
make test           # unit + e2e
make lint
make build-mock     # ./mockdlocal
make mock           # serve on 127.0.0.1:12112
make mock-dev ARGS="payments get D-4-rejected"
mockdlocal --routes # the mocked surface
```

`mockdlocal` **verifies the HMAC signature** on every request, so the e2e suite is evidence that
signing works end to end rather than just that output parses.

See `design-docs/initial-design.md` for the endpoint inventory and the reasoning behind the auth
model, and `design-docs/mock-dlocal.md` for the mock's contract.

## Known gaps

- **Payouts v3 is not supported.** It uses OAuth2 bearer tokens from `/oauth/token` rather than
  signatures, which is a second credential model. v2 covers the same read surface today.
- **No `installments-plans` retrieve command.** Creating a plan is a mutation, and the `GET`
  retrieve-by-id form is not confirmed in dLocal's docs. `api get` covers it if it exists.

## License

PolyForm Perimeter 1.0.0 — see `LICENSE`.
