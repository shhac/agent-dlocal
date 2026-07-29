---
name: agent-dlocal
description: |
  Investigate and triage dLocal payins, payouts, refunds, and chargebacks across LatAm, Africa, and Asia. Covers payment status, merchant order lookup, refund state, payout delivery, chargebacks, and per-country payment-method availability. Use when:
  - Explaining why a dLocal payment failed, was rejected, expired, or is still pending
  - Resolving a merchant order_id to a dLocal payment
  - Explaining what happened to a refund, or whether it was partial or full
  - Finding where a payout is and whether its status is final
  - Checking which payment methods or banks a country supports
  Triggers: "dlocal", "dLocal payment", "payin", "payout", "status_detail", "status_code", "X-Login", "X-Trans-Key", "Payload-Signature", "V2-HMAC-SHA256", "CPF", "PIX", "boleto", "PSE", "chargeback", "emerging markets payment", "LatAm payment"
allowed-tools: Bash(agent-dlocal *) Bash(mockdlocal *) Read Grep Glob
---

# agent-dlocal

Use `agent-dlocal` when investigating dLocal payment incidents: a payin that failed or stalled, a
payout whose whereabouts are unclear, a refund that has not landed, or a chargeback.

dLocal is an emerging-markets payment processor. Its API is **retrieve-by-id**: there are no list or
search endpoints, so every investigation starts from an id you already have.

## Safety

- **Never accept pasted dLocal credentials in chat.** A dLocal credential set is three secrets
  (X-Login, X-Trans-Key, Secret key). Ask the user to run
  `agent-dlocal auth add <profile> --form` locally, which collects them through native OS dialogs
  (one per secret, each titled with the value it wants) so they never enter the transcript.
- Use `agent-dlocal auth update <profile> --form` when a secret needs rotating.
- Never ask the tool to reveal a stored credential. There is no command that does this.
- Every command is read-only. dLocal refunds and payouts move real money in markets where reversal
  is slow or impossible — this CLI cannot write, by design.
- Use `--expose <path,key>` only when the user explicitly needs a redacted field. **`payer.document`
  is a national ID number** (CPF, CUIT, DNI); treat exposing it as a deliberate act, not a default.
  Stored credentials are never exposable.

## Start here

```bash
agent-dlocal usage
agent-dlocal investigate usage
agent-dlocal auth list
agent-dlocal auth check
```

## Prefer `investigate` for incident questions

When the user asks a question in incident language rather than naming an object, reach for
`investigate`. It chains several reads into a verdict plus the evidence, so you make one call
instead of correlating four records yourself.

```bash
agent-dlocal investigate payment <payment_id>   # Why did this payment fail?
agent-dlocal investigate order <order_id>       # They say they paid; our order says unpaid
agent-dlocal investigate refund <refund_id>     # What happened to this refund?
agent-dlocal investigate payout <payout_id>     # Where is this payout?
```

Each returns `verdict`, `terminal` (whether the state is final), `next_steps`, and `evidence`.

See `references/investigation/` for what each scenario reads and how to interpret it.

## Direct retrieval

```bash
agent-dlocal payments get <payment_id>...        # full record
agent-dlocal payments status <payment_id>...     # status triple only (12-month window)
agent-dlocal orders get <order_id>...            # merchant order -> payment
agent-dlocal refunds get <refund_id>...
agent-dlocal chargebacks get <chargeback_id>...
agent-dlocal payouts get <payout_id>...
agent-dlocal payment-methods list [COUNTRY...]      # one record per country
agent-dlocal payment-methods countries --supported  # which markets work at all
agent-dlocal api get <path> [--query k=v] [--payouts]
```

`get` takes multiple ids and returns one record per id in input order; `payment-methods` takes
countries the same way. `--country XX` is global — use it to switch market on any command that takes
one, rather than looking for a per-command spelling.

## Reading a dLocal outcome

dLocal reports outcomes as a triple: `status` (word), `status_code` (number), `status_detail`
(sentence). **Always read `status_detail`** — it carries the actual reason, while `status` only
carries the category.

| status | code | Final? | Means |
|---|---|---|---|
| `PENDING` | 100 | no | Awaiting processing or a customer action |
| `PAID` | 200 | yes | Paid |
| `REJECTED` | 300 | yes | Rejected — read `status_detail` for why |
| `CANCELLED` | 400 | yes | Cancelled by merchant or customer |
| `EXPIRED` | 600 | yes | Voucher window elapsed unpaid (cash/ticket methods) |

Payouts use a **different** table — code 500 means `DELIVERED` for a payout and nothing for a payin:

| status | code | Final? | Means |
|---|---|---|---|
| `PENDING` | 100 | no | Received, pending processing |
| `DELIVERED` | 500 | **no** | In flight at the beneficiary's bank |
| `PAID` | 200 | yes | Paid |
| `REJECTED` | 300 | yes | Rejected — often beneficiary account validation |
| `CANCELLED` | 400 | yes | Cancelled by the merchant |

> **`DELIVERED` is not a failure and not final.** It is the payout status most often misread. Never
> advise re-sending a payout on the strength of it — wait for `PAID` or `REJECTED`.

## Common traps

- **A `PENDING` REDIRECT payin usually means the customer never finished**, not that dLocal is slow.
  Check whether they reached the `redirect_url`.
- **`payments status` only works within 12 months** of the payment's creation date. Older payments
  404 there but may still resolve through `payments get`.
- **Live and sandbox are separate ledgers.** An id from one never resolves against the other, and a
  404 is often really an environment mix-up. dLocal keys carry no `test`/`live` marker, so check the
  profile: `agent-dlocal auth list`.
- **Read the dLocal `code`, not the HTTP status.** They disagree: a bad signature is
  `400 {"code":5000}` on payins, and `403 {"code":"authentication_failed"}` on payouts. Note payouts
  codes are *strings* while payins codes are *numbers*. `403 {"code":3001} Invalid credentials` is returned *before* the
  signature is checked, so it means the caller was rejected outright — most often the machine's IP
  is not on the dashboard's IP Whitelist for that product and environment, or the profile points at
  the wrong host.
- **Clock skew is NOT a failure mode**, despite the timestamp being part of the signature. `X-Date`
  is signed *and* sent, so a drifted clock stays self-consistent and validates fine.
- **There is no list-countries endpoint.** If the user asks which markets they can operate in, run
  `payment-methods countries --supported` — it probes each market. dLocal does **not** support
  Singapore, South Korea, Taiwan, Hong Kong, Venezuela, or western Europe; an unsupported code
  returns `400 {"code":5003}`.
- **`order_id` is the merchant's id, not dLocal's.** If the user gives you their own reference, use
  `orders get`, not `payments get`.

## Output contract

- NDJSON by default; `--format json|yaml` available.
- A missing id emits `{"@unresolved": …}` on stdout with exit 0 — a batch is not lost to one miss.
- Errors are `{"error", "fixable_by": "agent"|"human"|"retry", "hint"}` on stderr with exit 1.
  `fixable_by` tells you whether to retry, correct your own input, or ask the user.

See `references/commands.md` for the full surface and `references/output.md` for the contract in
detail.
