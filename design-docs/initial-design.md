# agent-dlocal initial design

## Goal

A read-only dLocal investigation CLI an LLM can drive to answer incident questions — why a payment
failed, where a payout went, what happened to a refund — with compact structured output and without
ever seeing a credential.

agent-dlocal is the dLocal counterpart to `agent-stripe` and inherits the family's contracts
wholesale: `lib-agent-cli` for the root command and native credential dialogs, `lib-agent-output`
for the format/redaction/error envelope, `lib-agent-mcp` for the MCP surface. Nothing in those
contracts is re-invented here; this document records only what is dLocal-specific.

---

## Endpoint inventory

**This is the design's foundation and its largest divergence from agent-stripe.** dLocal's read
surface is retrieve-by-id and status-oriented. There is no list endpoint, no search endpoint, and no
cursor pagination anywhere in the payins API. The command surface below follows from that fact
rather than from agent-stripe's shape.

### Payins — `https://api.dlocal.com` (live) / `https://sandbox.dlocal.com` (sandbox)

| Endpoint | Purpose | Verified |
|---|---|---|
| `GET /payments/{id}` | Full payment record: status triple, payer, card, amounts, order_id | yes |
| `GET /payments/{id}/status` | Lightweight status triple. **Only within 12 months of creation** — older payments 404 | yes |
| `GET /orders/{order_id}` | Merchant-order-id → order record (order_id, payment_id, status triple, amounts) | yes |
| `GET /refunds/{id}` | Refund record | yes |
| `GET /chargebacks/{id}` | Chargeback record | yes |
| `GET /payments-methods?country=XX` | Enabled payment methods for a country, incl. bank lists | yes |

### Payouts v2 — `https://marketplace-api.dlocal.com` (live) / `https://marketplace-api.dlocal-sbox.com` (sandbox)

| Endpoint | Purpose | Verified |
|---|---|---|
| `GET /v2/payouts/{id}` | Payout record and status | yes |

### Deliberately excluded

- **Every `POST`/`PATCH`.** Read-first — see [Read-first policy](#read-first-policy).
- **`POST /installments-plans`.** Creating a plan is a mutation. The `GET` retrieve-by-id form is
  not confirmed in the docs, so no command is built on it; `agent-dlocal api get` covers it if it
  exists.
- **Payouts v3.** See the divergence note below — it is a different auth system, not a different
  path, and pulling it in would double the credential model for a surface v2 already covers.
- **Sandbox-tools endpoints** (`PATCH /sandbox-tools/...`). Test-fixture mutation, not triage.

### Divergences from the build brief

The brief was written from a docs skim; where it and the docs disagree, the docs win. Two places
they disagree:

1. **Payouts v3 does not use `Payload-Signature`.** The brief says the Payouts API "signs
   differently (a `Payload-Signature` header)". That is true of **Payouts v2 only**. Payouts **v3**
   drops signatures entirely and uses OAuth2 Bearer tokens obtained from `/oauth/token`
   (`Authorization: Bearer <token>`). These are two different auth systems, so agent-dlocal targets
   **v2** — its HMAC signer is a near-sibling of the payins signer and needs no token cache, refresh
   loop, or second credential shape. v3 support is a deliberate follow-up, not an oversight.
2. **Payouts lives on a different host, not just a different path.** The brief anticipated "a
   different base path/version". It is a separate `marketplace-api.dlocal.com` host with its own
   sandbox domain (`dlocal-sbox.com`, note: not `dlocal.com`). The profile therefore carries two
   base URLs, not one.

---

## Auth and signing

### The payins signer

Every payins request carries:

| Header | Value |
|---|---|
| `X-Login` | merchant login identifier (secret) |
| `X-Trans-Key` | merchant transaction key (secret) |
| `X-Date` | ISO-8601 datetime with timezone, e.g. `2026-07-29T15:42:57.130Z` |
| `X-Version` | `2.1` |
| `User-Agent` | `agent-dlocal/<version>` |
| `Content-Type` | `application/json` |
| `Authorization` | `V2-HMAC-SHA256, Signature: <hex>` |

where

```
signature = hex_lower(HMAC_SHA256(secretKey, X-Login || X-Date || rawRequestBody))
```

For a GET the body is the empty string, so the signed message is `X-Login || X-Date`.

### The payouts v2 signer

Same secret, same HMAC-SHA256, same lowercase hex — but the signature travels in a
**`Payload-Signature`** header and covers the request payload. It is implemented as its **own
signer type** rather than a flag on the payins signer, so neither can silently acquire the other's
header set.

### The sign-exactly-what-you-send constraint

**The signature covers the exact bytes on the wire.** The client serializes a body **once** into a
`[]byte`, and that same slice is both signed and sent. There is no path that marshals for signing
and marshals again for sending — key ordering or whitespace drift between two marshals produces
intermittent 401s that look random and cost hours to diagnose.

This is enforced structurally: the request builder takes `body []byte` (never an `any` to be
marshalled downstream), and a unit test asserts byte-identity between the signed message and
`httptest`'s observed request body.

### Mutual TLS: optional, never required

dLocal enables client-certificate auth per merchant, layered *on top of* the HMAC headers. With no
certificate configured the client does plain TLS + HMAC. When configured, the profile carries
`cert_path` and `key_path`; the key passphrase lives in the keychain with the other secrets.

Note that mTLS also changes the sandbox host (`sandbox-cert.dlocal.com` rather than
`sandbox.dlocal.com`), which is another reason the host is explicit profile metadata rather than
something inferred.

### No key prefixes — environment is a host, not a tell

Stripe keys self-identify (`sk_test_` vs `sk_live_`), so agent-stripe can classify a credential by
inspecting it (`internal/credential/classify.go`). **dLocal keys carry no such tell.** There is no
`classify.go` in this repo and there should never be one: guessing an environment from an opaque
string would be a fabrication.

Live vs sandbox is therefore a **host** distinction, stored as explicit non-secret metadata on the
profile (`environment: live|sandbox`, plus the resolved `base_url` / `payouts_base_url`). `auth add`
defaults to `live` and takes `--sandbox` to opt out; nothing is inferred from the secrets.

### Credential storage

A dLocal credential set is three secrets (`X-Login`, `X-Trans-Key`, Secret key) plus an optional
key passphrase. They are marshalled to JSON and stored as **one opaque keychain item per profile**
under service `app.paulie.agent-dlocal`:

```json
{"login": "...", "trans_key": "...", "secret_key": "...", "key_passphrase": "..."}
```

One item, one `Remove`, no partial-write window. The non-secret `credentials.json` index keeps one
entry per profile recording only *where* the secret lives.

**Suffixed keychain names (`profile.login`, `profile.secret`, …) are explicitly rejected**: they
turn removal into a multi-delete that leaks every secret a `Remove` misses.

The credential package exposes no method that lists or prints secret values. Nothing but the signer
ever reads them.

### `--form` is the primary path

`--form` prompts for all secrets in a **single** native OS dialog — `dialog.Spec.Items` is already a
slice, so four `dialog.Password` fields need no library change:

```
agent-dlocal auth add prod --cert ~/.dlocal/client.pem --key ~/.dlocal/client.key --form
```

The secrets go from the user's keyboard straight into the OS keychain; they never enter a chat
transcript or a model's context.

**The certificate is not a form field.** `dialog.InputType` is `Text | Password` — single-line
entries only. A PEM or `.jks` cannot sanely be typed into one, and a *path* is not a secret. So
`--cert`/`--key` take paths, stored as non-secret profile metadata; the key file stays where the
user put it under their own file permissions. **The key passphrase is a form field** — short,
single-line, genuinely secret.

Non-interactive equivalents (`--login`, `--trans-key`, `--secret-key`, `--key-passphrase`) exist for
automation and tests, but the README and SKILL steer humans and LLMs to `--form`.

Dialog failures are classified with `dialog.ClassifyError` into `fixable_by` + hint, including a
non-interactive fallback hint that names the explicit-flag form (a headless agent cannot open a
dialog and must be told what to do instead).

### `auth check`

dLocal has no `/account` endpoint, so there is no natural "who am I" call. `auth check` instead
issues `GET /payments-methods?country=<--country, default BR>` — the cheapest authenticated read in
the API. It proves the login, trans-key, secret, clock skew, and signature construction are all
correct in one round trip, which is exactly what a credential check is for.

---

## Read-first policy

**No mutating command ships without a design document that explicitly approves it.** dLocal refunds
and payouts move real money in markets where reversal is slow, manual, or impossible. Every command
in the surface below is a `GET`. The `api` escape hatch is `GET`-only by construction — it has no
`--method` flag to set to `POST`.

---

## Output contract

Inherited wholesale from `lib-agent-output` / `lib-agent-cli`. No variant is invented.

- NDJSON by default; `--format json|yaml` available.
- `get <id>...` accepts multiple ids and emits one record per id **in input order**. A miss emits an
  `{"@unresolved": …}` line **on stdout with exit 0** — a 404 for one id must not abort a batch.
  Only command-level failures (bad credentials, no profile, network death) go to stderr with exit 1,
  rendered once by the single sink in `libcli.Run`.
- Sensitive fields are redacted by default; `--expose <path,key>` opts out per invocation. Stored
  credentials are never exposable.
- Errors are `{"error", "fixable_by": "agent"|"human"|"retry", "hint"}` on stderr.

### Redaction policy

dLocal responses carry more raw PII than Stripe's — the `payer.document` field is a national ID
number (CPF, CUIT, DNI, …), which is both directly identifying and legally sensitive in most of
dLocal's markets. Redacted by default:

`payer.name`, `payer.email`, `payer.phone`, `payer.document`, `payer.user_reference`,
`payer.address.*`, `payer.ip`, `payer.device_id`, `card.number`, `card.cvv`, `card.holder_name`,
`beneficiary.*`, `bank_account.*`, plus the family's substring rules (`*secret*`, `*token*`,
`*password*`, `*api_key*`).

`card.last4`, `card.brand`, `card.bin`, and the status triple are **not** redacted — they are what
triage runs on.

Debug output (`--debug`) redacts the `Authorization` / `Payload-Signature` header value and the
`X-Login` / `X-Trans-Key` header values. The signature is a secret-derived value; echoing it into a
transcript would leak an oracle.

### Error mapping — the status triple

Mapping dLocal's `status` / `status_code` / `status_detail` triple into an actionable hint is a
large part of this tool's value, so it is a designed table rather than an improvisation.

Payins status codes:

| Code | Status | Meaning |
|---|---|---|
| 100 | `PENDING` | Received, awaiting processing or customer action |
| 200 | `PAID` | Paid |
| 300 | `REJECTED` | Rejected |
| 400 | `CANCELLED` | Cancelled |
| 600 | `EXPIRED` | Payment window elapsed (cash/ticket methods) |

Payouts status codes:

| Code | Status | Meaning |
|---|---|---|
| 100 | `PENDING` | Received by dLocal, pending processing |
| 500 | `DELIVERED` | Being processed by the receiving institution |
| 200 | `PAID` | Paid |
| 300 | `REJECTED` | Rejected |
| 400 | `CANCELLED` | Cancelled by the merchant |

HTTP-level classification:

| HTTP | `fixable_by` | Hint |
|---|---|---|
| 401 | `human` | Signature/credential failure. Names the three usual causes: wrong secret, **clock skew on `X-Date`**, or a body that changed between signing and sending |
| 403 | `human` | Credential lacks permission, or the merchant is not enabled for this country/method |
| 404 | `agent` | Check the id, the environment (live vs sandbox are separate ledgers), and for `payments/{id}/status` the 12-month retention window |
| 429 | `retry` | Rate limited; retried `--max-retries` times already |
| 5xx | `retry` | dLocal server error |

The 401 hint calling out clock skew is deliberate: `X-Date` is inside the signed message, so a
machine with a drifted clock produces a valid-looking signature that dLocal rejects. It is the
single most confusing failure mode in this API and the agent should not have to rediscover it.

### Retries

Bounded retries with exponential backoff + jitter on transient 429 and 5xx. `--max-retries`
defaults to 2. `Retry-After` is honoured when present.

---

## Command surface

```
agent-dlocal auth add|update|check|list|default|remove
agent-dlocal config show|path|get|set|unset
agent-dlocal usage

agent-dlocal payments get <payment_id>...        GET /payments/{id}
agent-dlocal payments status <payment_id>...     GET /payments/{id}/status
agent-dlocal payments usage

agent-dlocal orders get <order_id>...            GET /orders/{order_id}
agent-dlocal refunds get <refund_id>...          GET /refunds/{id}
agent-dlocal chargebacks get <chargeback_id>...  GET /chargebacks/{id}
agent-dlocal payouts get <payout_id>...          GET /v2/payouts/{id}   (payouts host)

agent-dlocal payment-methods list --country XX   GET /payments-methods?country=XX

agent-dlocal investigate payment <payment_id>
agent-dlocal investigate order <order_id>
agent-dlocal investigate refund <refund_id>
agent-dlocal investigate payout <payout_id>
agent-dlocal investigate usage

agent-dlocal api get <path> [--query k=v]        GET-only escape hatch
agent-dlocal mcp
```

Note the verbs: `get` and `status`, never `list`, because no list endpoint exists.
`payment-methods list` is the sole exception and it is a per-country lookup, not a paginated
collection — it takes a required `--country` and returns an array.

### `investigate` — the highest-value group

Incident-language entry points that chain several reads into one answer, so the agent makes one call
instead of four and gets a synthesized verdict rather than raw records to correlate.

- **`investigate payment <id>`** — fetches the payment; if `REJECTED`/`CANCELLED`, explains the
  status triple against the code table; if the method is a card, surfaces `brand`/`last4`/BIN
  context; checks for an associated chargeback and refund. Answers *"why did this payment fail?"*
- **`investigate order <order_id>`** — resolves the merchant order id to its payment, then runs the
  payment analysis. Answers *"the customer says they paid, our order says unpaid."*
- **`investigate refund <id>`** — fetches the refund and its parent payment, compares amounts
  (partial vs full), and reports where a `PENDING` refund is stuck. Answers *"what happened to this
  refund?"*
- **`investigate payout <id>`** — fetches the payout from the payouts host and maps its status
  against the payout code table, distinguishing `DELIVERED` (in flight at the receiving bank — not a
  failure, just not final) from `PAID`. Answers *"where is this payout?"*

Each scenario emits a top-level `verdict` plus the `evidence` records it drew on, and each gets a
reference doc under `skills/agent-dlocal/references/investigation/`.

`DELIVERED` deserves special mention: it is the payout status most often misread as terminal. The
investigate output says explicitly that it is in flight.

### MCP

`agentmcp.Expose` opts in the agent-facing groups only — `payments`, `orders`, `refunds`,
`chargebacks`, `payouts`, `payment-methods`, `investigate`, `api`. `auth`, `config`, and `usage` are
left out: they are operator tasks, not agent tasks, and exposing `auth` to a tool-calling loop is
how credentials get written by something that shouldn't be writing them.

`agentmcp.Command(root, …)` is added **last**, so the generated schema reflects the complete tree.

---

## Configuration

| Thing | Value |
|---|---|
| Config dir | `xdg.ConfigDir("agent-dlocal")` → `~/.config/agent-dlocal/` |
| Keychain service | `app.paulie.agent-dlocal` (MCP OAuth under `app.paulie.agent-dlocal.mcp`) |
| Env prefix | `AGENT_DLOCAL_PROFILE`, `AGENT_DLOCAL_BASE_URL`, `AGENT_DLOCAL_NO_KEYCHAIN` |
| Direct-credential env | `DLOCAL_X_LOGIN`, `DLOCAL_X_TRANS_KEY`, `DLOCAL_SECRET_KEY` |
| Global config keys | `timeout_ms`, `max_retries` |

Profile metadata (all non-secret): `environment`, `base_url`, `payouts_base_url`, `cert_path`,
`key_path`, `country` (default for `auth check` / `payment-methods`).

---

## dLocal docs checked

- Generate a signature — `https://docs.dlocal.com/docs/generate-signature`
- Initial settings (incl. mTLS client-cert example) — `https://docs.dlocal.com/docs/initial-settings`
- Get payment — `https://docs.dlocal.com/reference/retrieve-a-payment`
- Get payment status — `https://docs.dlocal.com/reference/retrieve-a-payment-status`
- Retrieve an order — `https://docs.dlocal.com/api-documentation/payins-api-reference/orders`
- Refunds — `https://docs.dlocal.com/api-documentation/payins-api-reference/refunds`
- Chargebacks / after payment — `https://docs.dlocal.com/docs/after-payment`
- Payment methods — `https://docs.dlocal.com/docs/direct-smartpix`
- Payouts v2 status codes — `https://docs.dlocal.com/reference/payouts-status-v2-platforms`
- Payouts v3 overview (OAuth2) — `https://docs.dlocal.com/docs/overview-payouts-v3`
- Make a test payment (status triple fixtures) — `https://docs.dlocal.com/docs/make-a-test-payment`
