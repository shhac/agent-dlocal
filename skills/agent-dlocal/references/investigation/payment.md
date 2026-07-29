# investigate payment

```
agent-dlocal investigate payment <payment_id>
```

Answers: **why did this payment fail?** — or, more precisely, what state is it in, is that state
final, and what should happen next.

## What it reads

`GET /payments/{id}` — one call. The synthesis, not the fetching, is the value.

## What it returns

| Field | Meaning |
|---|---|
| `verdict` | The status explained, with dLocal's own `status_detail` quoted |
| `terminal` | Whether the state is final. **The most important field.** |
| `next_steps` | What to do, keyed to the specific status and payment method |
| `evidence.payment` | The full record the verdict was drawn from |

## Interpreting it

**`terminal: false`** means the payment is not finished. Do not describe it as failed. The two
common cases:

- `PENDING` on a `REDIRECT` flow — the customer almost certainly never completed the hosted step.
  The command says so explicitly. Check whether they reached `redirect_url`.
- `PENDING` on a cash or ticket method (boleto, OXXO) — the voucher exists and is unpaid. Nothing is
  wrong; the customer has not paid yet.
- `AUTHORIZED` — the card authorization succeeded but capture never ran. This is a merchant-side
  gap, not a dLocal failure.

**`terminal: true` with `REJECTED`** — read `status_detail`. It distinguishes an issuer decline
(retryable with a different instrument) from a validation or fraud-rule rejection (not retryable as
is). For card payments the command surfaces `brand` and `bin`, which is what you need to tell the
user which card was declined without revealing the PAN.

**`EXPIRED`** — the voucher window elapsed. This payment cannot be revived; a new one is required.

**`CANCELLED`** — merchant- or customer-initiated, not an issuer decision. Check your own
cancellation path before blaming dLocal.

## If it 404s

Three causes, in likelihood order:

1. Wrong environment — live and sandbox are separate ledgers.
2. The id is a merchant `order_id`, not a dLocal `payment_id`. Use `investigate order` instead.
3. The id is genuinely wrong.
