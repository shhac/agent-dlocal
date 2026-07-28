# mockdlocal design

## Purpose

A local HTTP server returning dLocal-shaped JSON so `agent-dlocal` can be exercised end to end
without credentials, network, or a sandbox merchant account. It mirrors `mockstripe` in
agent-stripe.

**The defining requirement: mockdlocal verifies the HMAC signature on every request.** A mock that
accepted any `Authorization` header would let a signing bug ship — the signature is the single most
failure-prone part of this integration and the only way to prove it correct is to have something
recompute it. Because mockdlocal recomputes `HMAC(secret, X-Login || X-Date || body)` and compares,
a passing e2e test is real evidence that the client signs correctly.

## Scope

### Routes

Payins (served at the root):

| Route | Behavior |
|---|---|
| `GET /payments/{id}` | Full payment fixture selected by id prefix (see Fixtures) |
| `GET /payments/{id}/status` | The status triple only |
| `GET /orders/{order_id}` | Order fixture linking `order_id` → `payment_id` |
| `GET /refunds/{id}` | Refund fixture |
| `GET /chargebacks/{id}` | Chargeback fixture |
| `GET /payments-methods?country=XX` | Payment-method array for the country |

Payouts (same server, so one `httptest.Server` covers both hosts in tests):

| Route | Behavior |
|---|---|
| `GET /v2/payouts/{id}` | Payout fixture; verifies the `Payload-Signature` header instead of `Authorization` |

Meta:

| Route | Behavior |
|---|---|
| `GET /` | Route inventory as JSON — the same list `mockdlocal --routes` prints |

An unknown path returns dLocal's 404 shape, not Go's default `404 page not found`.

### Authentication enforcement

Every route except `GET /` enforces, in order:

1. **Presence.** `X-Login`, `X-Trans-Key`, `X-Date`, and the signature header must all be present.
   A missing one returns `401` with a body naming which header was absent — the mock's error is a
   teaching tool, so it says what is wrong rather than just refusing.
2. **Signature.** Recompute `hex_lower(HMAC_SHA256(secret, X-Login || X-Date || body))` over the
   **raw request body bytes as received** and compare in constant time. A mismatch returns `401`
   with `status_code` `401` and a detail distinguishing "signature mismatch" from "missing header".
3. **Clock skew.** `X-Date` must parse as ISO-8601 and sit within a configurable window
   (`--max-skew`, default 5 minutes). Outside it, `401` with a detail naming clock skew — mirroring
   the real API's most confusing failure so the hint text can be tested.

The expected credentials default to `mocklogin` / `mocktrans` / `mocksecret` and are overridable
with `--login`, `--trans-key`, `--secret-key`.

The payouts route verifies `Payload-Signature` and rejects a request that carries a payins-style
`Authorization` header instead, so the two signers cannot be confused without a test failing.

### Deliberately out of scope

- No `POST`/`PATCH` routes. The CLI is read-only, so the mock has nothing to mutate.
- No persistence. Fixtures are in-memory and identical on every start.
- No TLS or mTLS. The client's certificate plumbing is unit-tested against a `tls.Config`; making
  the mock do a client-cert handshake would test `crypto/tls`, not agent-dlocal.

## CLI wiring

```
mockdlocal [--addr 127.0.0.1:12112] [--routes] [--max-skew 5m]
           [--login mocklogin] [--trans-key mocktrans] [--secret-key mocksecret]
```

- `--routes` prints the route inventory and exits — the same list `GET /` serves and the same list
  embedded in the command's long help.
- On start it prints `{"status":"listening","base_url":"http://127.0.0.1:12112"}` to stdout so a
  supervising script can wait on one line of JSON.

Make targets:

- `make build-mock` → `./mockdlocal`
- `make mock` → runs it on `127.0.0.1:12112`
- `make mock-dev ARGS="payments get D-4-paid"` → runs the CLI against it with the mock credentials
  preset in the environment

Port `12112` is one above agent-stripe's `12111`, so both mocks can run side by side.

## Fixtures

Fixtures are selected by **id suffix**, so a test names the scenario it wants rather than
memorizing an opaque id. `D-4-paid` returns a paid payment; `D-4-rejected` returns a rejected one.

| Id suffix | Scenario |
|---|---|
| `-paid` | `PAID` / `200` / "The payment was paid" |
| `-pending` | `PENDING` / `100`, with a redirect URL |
| `-rejected` | `REJECTED` / `300` — the primary failure-triage fixture |
| `-cancelled` | `CANCELLED` / `400` |
| `-expired` | `EXPIRED` / `600`, cash/ticket method |
| `-chargeback` | `PAID` payment that also resolves a chargeback record |
| `-refunded` | `PAID` payment with an associated refund |
| (anything else) | `404` in dLocal's error shape |

Payout fixtures use the same convention with the payout status set: `-paid`, `-pending`,
`-delivered`, `-rejected`, `-cancelled`.

Every payment fixture carries a populated `payer` block — `name`, `email`, `document`, `address` —
specifically so the redaction tests have real PII-shaped values to assert are masked. A fixture with
empty payer fields would let a redaction regression pass silently.

Card fixtures carry `brand`, `last4`, and `bin` alongside a full `number`, so tests can assert that
the triage-relevant fields survive redaction while the pan does not.
