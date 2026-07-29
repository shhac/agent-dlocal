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
| `GET /payments-methods?country=XX` | Enabled payment methods for a country | yes |

Response shape, verified live: each entry carries exactly `id`, `type`, `name`, `logo`,
`allowed_flows`. There is no `country` echo and no `details.banks` block on a plain country query,
though the docs show one for specific methods (SmartPix, PSE).

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

### One signer, one client shape

Payins and payouts share the signer AND the client. `Host` is a value
(`HostPayins` / `HostPayouts`) selecting which base URL to read, and `Session`
carries the client plus the non-secret profile metadata a command may report.

> **Simplified after the signer finding.** There were once eight entry points in
> `internal/cli/shared` — `WithClient`/`WithPayoutsClient`,
> `GetEntities`/`GetPayoutEntities`, `GetRawItem`/`GetPayoutRawItem`, plus
> `WithResolvedClient` and `WithResolvedProfile` — varying along one axis. They
> were parallel constructors from when payouts genuinely needed a different
> signer as well as a different host. Once the signer difference was disproved
> only a URL field remained, but the function-per-variant shape survived.
> Adding a third host is now a constant, not five functions.
>
> `WithSessionResult[T]` exists because the error-only contract forced every
> command that produces a value to declare an outer variable and assign it
> inside a closure — a workaround all four investigate commands had copied.

### The payouts signer: there isn't one

> **Correction, from live testing.** This section originally specified a separate payouts signer
> using a `Payload-Signature` header over the body alone, per
> `https://docs.dlocal.com/docs/generate-signature`. Tested against the sandbox payouts host, that
> scheme is **rejected**:
>
> | Sent to `marketplace-api.dlocal-sbox.com/v2/payouts/{id}` | Result |
> |---|---|
> | `Payload-Signature` = HMAC(body) | `401 invalid_credentials` |
> | `Payload-Signature` = HMAC(login+date) | `401 invalid_credentials` |
> | no signature header | `401 invalid_credentials` |
> | `Authorization: V2-HMAC-SHA256` (payins) | `404 payout_not_found_id` — **auth passed** |
> | `Authorization` with a corrupted digest | `403 authentication_failed` |
>
> The corrupted-digest control confirms the signature is genuinely verified rather than ignored.
>
> **Payouts therefore differ from payins by HOST ONLY.** `PayoutsSigner` is deleted. The `Signer`
> interface survives because Payouts **v3** — OAuth2 bearer tokens instead of signatures — would be
> a real second implementation. A test asserts no `Payload-Signature` header is ever sent, so the
> documented-but-wrong scheme cannot be reinstated from the docs.

### The payouts error shape differs

The two APIs do not share an error contract, which matters because a struct typed for one silently
fails to parse the other and loses the message:

| | payins | payouts |
|---|---|---|
| `code` | number (`5000`) | **string** (`"payout_not_found_id"`) |
| offending field key | `param` | **`field`** |
| bad signature | `400` / `5000` | `403` / `authentication_failed` |
| caller rejected | `403` / `3001` | `401` / `invalid_credentials` |

The client decodes `code` leniently and accepts either field key.

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
`cert_path` and `key_path`. The key must be unencrypted PEM: `tls.LoadX509KeyPair` cannot decrypt
one, so there is nothing a passphrase could be used for.

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

A dLocal credential set is three secrets: `X-Login`, `X-Trans-Key`, and the Secret key. They are
marshalled to JSON and stored as **one opaque keychain item per profile**
under service `app.paulie.agent-dlocal`:

```json
{"login": "...", "trans_key": "...", "secret_key": "..."}
```

One item, one `Remove`, no partial-write window. The non-secret `credentials.json` index keeps one
entry per profile recording only *where* the secret lives.

**Suffixed keychain names (`profile.login`, `profile.secret`, …) are explicitly rejected**: they
turn removal into a multi-delete that leaks every secret a `Remove` misses.

The credential package exposes no method that lists or prints secret values. Nothing but the signer
ever reads them.

### `--form` is the primary path

`--form` prompts for each missing secret in a native OS dialog, **one field per dialog, with the
field name in the dialog's title**:

```
agent-dlocal auth add prod --cert ~/.dlocal/client.pem --key ~/.dlocal/client.key --form
```

The secrets go from the user's keyboard straight into the OS keychain; they never enter a chat
transcript or a model's context.

> **Corrected after first use.** The original design said "all secrets in a *single* dialog",
> reasoning that `dialog.Spec.Items` is a slice so a multi-field Spec would render as one form. Both
> halves were wrong, and the result shipped in v0.1.0:
>
> 1. `dialog.Prompt` already renders a multi-item Spec as a **chain** of dialogs — one per field,
>    titled `(step N of M)`. There is no combined-form mode.
> 2. For a `Password` field with no `Initial`, the zenity backend calls `zenity.Password`, which
>    renders a fixed `Password:` body and **discards `Field.Label`**. The label survives only in
>    error messages.
>
> Together those produced three identical unlabelled `Password:` boxes numbered 1..3, with nothing
> saying which secret each wanted. agent-stripe never hit this because it has exactly one secret and
> its title says which. agent-dlocal is the family's first multi-secret tool.
>
> The fix keeps everything inside this repo: each field gets its own single-item Spec with a
> self-describing title (`agent-dlocal · <profile> · X-Trans-Key (2 of 3)`). A single-item Spec also
> suppresses the library's own `(step N of M)` suffix, so the title is entirely ours.
>
> The root fix belongs in `lib-agent-cli`: `promptOne` could use
> `zenity.Entry(label, HideText())` — which honours the label *and* masks input, as the
> `Initial != ""` branch already proves — instead of `zenity.Password`. That is a change across 14
> repos, so it is proposed rather than assumed.

**The certificate is not a form field.** `dialog.InputType` is `Text | Password` — single-line
entries only. A PEM or `.jks` cannot sanely be typed into one, and a *path* is not a secret. So
`--cert`/`--key` take paths, stored as non-secret profile metadata; the key file stays where the
user put it under their own file permissions.

> **Removed after the structural review.** There was a fourth secret here — a client-key passphrase,
> collected by `--key-passphrase`, by a dialog field, and by `DLOCAL_KEY_PASSPHRASE`, backfilled on
> update and persisted to the keychain. **Nothing ever read it.** mTLS goes through
> `tls.LoadX509KeyPair`, which cannot decrypt an encrypted key — the error hint already told users
> the key had to be unencrypted PEM. Prompting a user for a secret, storing it, and discarding it is
> worse than not offering the feature, so the whole path is gone and the hint now names the
> `openssl pkey` fix. Original text follows for the record: **The key passphrase is a form field** — short,
single-line, genuinely secret.

Non-interactive equivalents (`--login`, `--trans-key`, `--secret-key`) exist for
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

- NDJSON by default — for single records as well as lists, via `libcli.EmitItem`, the family's
  canonical emitter, whose doc is explicit that a single record "should still default to NDJSON like
  every other get". `--format json|yaml` gives the pretty bare object (single) or `{"data":[…]}`
  envelope (list).

> **Corrected during the structural review.** `shared.WriteItem` defaulted to pretty JSON, copied
> from agent-stripe's older emitter. That contradicted this document, the README, `usage`, and the
> rest of the family — agent-notion and agent-slack already funnel through `EmitItem`. Six call
> sites also passed a hardcoded `""` for the format, so `--format` was silently ignored on eight
> commands.
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

Debug output (`--debug`) logs the request URL, status, signer scheme and response body. It does
**not** log request headers at all, so the signature and credential headers have no path to a
transcript — the strongest available guarantee, and simpler than masking them.

> An earlier version carried a `SafeHeaders` masking helper for this. Nothing called it: `logDebug`
> never logged request headers, so the layer masked something that was never emitted while implying
> to readers that request headers *were* being logged safely somewhere. Deleted.

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

**Classification keys off the dLocal `code`, not the HTTP status** — the two disagree, and the
status alone is misleading. Every row below was observed against `sandbox.dlocal.com`:

| dLocal code | HTTP | `fixable_by` | Meaning |
|---|---|---|---|
| 5000 | **400** | `human` | Signature did not match. Usually a truncated secret key. |
| 3001 | 403 | `human` | Caller rejected *before* the signature is checked: IP not allowlisted, wrong host for the credential, or wrong X-Login |
| 5001 | 400 | `agent` | Missing/malformed parameter or header; the body carries `param` naming it |
| 4000 | 404 | `agent` | Payment / order / chargeback not found |
| 4001 | 404 | `agent` | Refund not found (note the different code) |
| — | 429 | `retry` | Rate limited |
| — | 5xx | `retry` | dLocal server error |

> **Correction: clock skew is not a failure mode.** An earlier version of this document claimed a
> drifted clock produces "a valid-looking signature that dLocal rejects", and made that the headline
> 401 hint. It is wrong. `X-Date` is signed *and* sent, so a wrong clock yields a **self-consistent**
> signature that validates: the sandbox accepts a date a year stale and a month in the future, both
> 200. Skew only matters if the date used for signing differs from the date sent — a code bug, which
> the single-serialization design already prevents. The hint now rules skew *out* explicitly, because
> the plausible-sounding wrong answer costs more than no answer.

> **Correction: a bad signature is a 400, not a 401.** The API returns no 401 at all on the paths
> this CLI uses. A rejection is 400/5000 (signature), 400/5001 (parameter), or 403/3001 (caller).
> Because 403/3001 is returned before signature evaluation, a *deliberately corrupted* signature and
> a *blocked IP* are byte-identical responses — so that error can never be used to confirm a
> credential is correct.

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

All of the above was re-verified against the live sandbox on 2026-07-29; where the docs and observed
behaviour differ, the observed behaviour is recorded and the divergence noted.

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
