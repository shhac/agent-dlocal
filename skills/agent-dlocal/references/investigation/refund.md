# investigate refund

```
agent-dlocal investigate refund <refund_id>
```

Answers: **what happened to this refund?** — including the question the refund record alone cannot
answer: was it partial or full?

## What it reads

1. `GET /refunds/{id}` — the refund record.
2. `GET /payments/{payment_id}` — the parent payment, so the amounts can be compared.

If the parent lookup fails, the verdict degrades rather than disappearing: the refund record is
still returned, with `evidence.payment_lookup_error` explaining what could not be fetched.

## What it returns

| Field | Meaning |
|---|---|
| `verdict` | The refund status with `status_detail` quoted |
| `next_steps` | Includes an explicit **PARTIAL** or **FULL** classification |
| `evidence.refund` | The refund record |
| `evidence.payment` | The parent payment |

## Interpreting it

**`PENDING`** is normal, not stuck. Card refunds settle quickly; cash and bank-transfer refunds are
paid out to the customer as a bank transfer and settle asynchronously, often over days. Only treat a
`PENDING` refund as a problem if it has sat for longer than the method's usual window.

**`REJECTED`** — read `status_detail`. Two usual causes: the original payment is not in a refundable
state, or the customer's bank details failed validation. The first is not fixable; the second is.

**Partial vs full** is the question users most often actually mean when they ask about a refund. The
command compares the refund amount against the payment amount and says which it is, so you do not
have to infer it from two numbers in different records.

## Note

A refund is not the same as a chargeback. If the customer disputed the charge with their bank rather
than asking the merchant, look for a chargeback: `agent-dlocal chargebacks get <id>`.
