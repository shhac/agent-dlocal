# agent-dlocal output contract

## Formats

NDJSON (`jsonl`) by default — one record per line, so a batch streams and greps cleanly.
`--format json` and `--format yaml` produce a single document.

## Multi-get

`get` accepts several ids and returns **one record per id, in input order**:

```
$ agent-dlocal payments get D-4-aaa D-4-nosuch D-4-bbb
{"id":"D-4-aaa","status":"PAID",...}
{"@unresolved":{"id":"D-4-nosuch",...}}
{"id":"D-4-bbb","status":"REJECTED",...}
```

A miss is an `@unresolved` line **on stdout with exit 0**. One bad id does not lose the rest of the
batch. Only command-level failures — no profile, bad credentials, network death — go to stderr with
exit 1.

## Errors

```json
{"error": "Authentication failed: Invalid signature", "fixable_by": "human", "hint": "..."}
```

`fixable_by` tells you what to do:

| Value | Meaning |
|---|---|
| `agent` | You can fix it: correct the id, the flag, the country code |
| `human` | The user must act: credentials, permissions, a desktop dialog |
| `retry` | Transient: rate limit, 5xx, network |

## Redaction

Sensitive fields are masked as `[REDACTED]` by default, with a `@redacted` note listing what was
hidden.

**Masked:** the whole `payer` and `beneficiary` blocks (`name`, `email`, `document`, `phone`,
`user_reference`, `ip`, `device_id`, `address.*`), `bank_account.*`, `card.number`, `card.cvv`,
`card.holder_name`, and anything matching `*secret*`, `*token*`, `*password*`, `*trans_key*`,
`*signature*`.

**Not masked:** `status`, `status_code`, `status_detail`, `amount`, `currency`, `country`, `id`,
`order_id`, `card.brand`, `card.last4`, `card.bin`. Triage runs on these.

`--expose payer.email` reveals one field. `--expose all` reveals everything.

> `payer.document` is a national ID number (CPF, CUIT, DNI, …) — directly identifying and legally
> sensitive across dLocal's markets. Expose it only when the user has explicitly asked for it.

Stored credentials are never exposable by any flag.

## Debug

`--debug` writes `@debug` lines to stderr with the request URL, status, signer scheme, and response
body. The `Authorization`, `Payload-Signature`, `X-Login`, and `X-Trans-Key` header values are
masked unconditionally — the signature is derived from the secret key, so echoing it would hand out
an oracle.
