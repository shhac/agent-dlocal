# investigate order

```
agent-dlocal investigate order <order_id>
```

Answers: **the customer says they paid, but our order shows unpaid — what happened?**

`order_id` is the id **your** system assigned, not dLocal's. This is the entry point when the user
has a merchant-side reference rather than a `D-4-…` payment id.

## What it reads

1. `GET /orders/{order_id}` — resolves the merchant reference.
2. `GET /payments/{payment_id}` — the payment it points at.

It then runs the same analysis as `investigate payment`, so the verdict, `terminal` flag, and
`next_steps` mean exactly what they mean there. See `payment.md`.

## What it returns

| Field | Meaning |
|---|---|
| `scenario` | `order` |
| `subject` | The order id you passed |
| `verdict` / `terminal` / `next_steps` | The payment analysis |
| `evidence.order` | The order record |
| `evidence.payment` | The payment record |

## The two informative failures

**The order exists but has no `payment_id`.** dLocal received an order but no payment was ever
created against it. The verdict says so, and the fault is on the merchant side — the code path that
submits the payment never ran or failed before reaching dLocal. This is a genuinely different
problem from a rejected payment, and it is worth naming clearly to the user.

**The order 404s.** dLocal has never heard of this reference. Either the order id is wrong, or the
integration never reached dLocal at all, or you are querying the wrong environment.

## Why this exists

Users report incidents with the reference they have in front of them, which is almost always their
own order number from their own admin panel. Making them find a dLocal payment id first is the kind
of friction that turns a one-step answer into a three-step interrogation.
