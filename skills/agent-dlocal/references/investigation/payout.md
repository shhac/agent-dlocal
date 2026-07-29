# investigate payout

```
agent-dlocal investigate payout <payout_id>
```

Answers: **where is this payout?**

## What it reads

`GET /v2/payouts/{id}` on the payouts host, signed with the `Payload-Signature` scheme. Same profile
credentials as payins; different host and different signature construction, handled transparently.

## What it returns

| Field | Meaning |
|---|---|
| `verdict` | The payout status explained, with `status_detail` quoted |
| `terminal` | Whether the state is final |
| `next_steps` | What to do |
| `evidence.payout` | The full record |

## The one thing to get right

**`DELIVERED` (code 500) is not final and not a failure.** It means dLocal has handed the money to
the beneficiary's bank and that bank is processing it. It is the payout status most often misread as
terminal, and the misreading is expensive: re-sending a payout that is merely in flight duplicates a
real disbursement.

`investigate payout` reports `terminal: false` for `DELIVERED` and says in `next_steps` that the
money is in flight. Wait for `PAID` or `REJECTED`.

## The rest of the table

| status | code | Final? | What to do |
|---|---|---|---|
| `PENDING` | 100 | no | dLocal has it, processing has not started. Wait. |
| `DELIVERED` | 500 | **no** | In flight at the beneficiary bank. Wait. Do not re-send. |
| `PAID` | 200 | yes | Done. |
| `REJECTED` | 300 | yes | Read `status_detail` — usually beneficiary account validation, which is fixable by correcting the details and re-sending. |
| `CANCELLED` | 400 | yes | The merchant cancelled it. |

## Note on beneficiary data

The `beneficiary` and `bank_account` blocks are redacted by default. They contain the recipient's
name, national ID, and account number. Expose them only if the user explicitly needs to verify the
account details against their own records.
