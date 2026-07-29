# agent-dlocal command reference

Every command is a `GET`. There is no way to write through this CLI.

## Global flags

| Flag | Meaning |
|---|---|
| `-p, --profile` | Profile alias (or `AGENT_DLOCAL_PROFILE`) |
| `-f, --format` | `jsonl` (default), `json`, `yaml` |
| `-t, --timeout` | Request timeout in milliseconds |
| `--max-retries` | Automatic retries for 429/5xx (default 2) |
| `--expose` | Reveal redacted fields by path or key (repeatable; `all` for everything) |
| `-d, --debug` | Diagnostics on stderr, with credentials masked |

## Setup

| Command | Purpose |
|---|---|
| `auth add <profile> --form` | Store credentials via native OS dialogs, one per secret. **Preferred.** |
| `auth add <profile> --login … --trans-key … --secret-key …` | Non-interactive equivalent, for automation |
| `auth add <profile> --sandbox` | Point the profile at sandbox rather than live |
| `auth add <profile> --cert <path> --key <path>` | Enable mutual TLS (paths are stored, not file contents) |
| `auth update <profile> --form` | Rotate the stored secrets |
| `auth check [profile] [--country XX]` | Verify credentials with one authenticated read |
| `auth list` | Profile metadata; never reads a secret value |
| `auth default <profile>` | Set the default profile |
| `auth remove <profile>` | Delete the profile and its credentials |
| `config show \| path \| get \| set \| unset` | Non-secret defaults (`timeout_ms`, `max_retries`) |

`auth check` calls `GET /payments-methods` — dLocal has no `/account` endpoint, and this is the
cheapest authenticated read that proves login, trans-key, secret, clock skew, and signature
construction all work.

## Retrieval

| Command | Endpoint | Notes |
|---|---|---|
| `payments get <id>...` | `GET /payments/{id}` | Full record |
| `payments status <id>...` | `GET /payments/{id}/status` | Triple only; 12-month window |
| `orders get <order_id>...` | `GET /orders/{order_id}` | Merchant reference → payment |
| `refunds get <id>...` | `GET /refunds/{id}` | |
| `chargebacks get <id>...` | `GET /chargebacks/{id}` | |
| `payouts get <id>...` | `GET /v2/payouts/{id}` | Separate host and signer |
| `payment-methods list --country XX` | `GET /payments-methods?country=XX` | The only list-shaped command |
| `api get <path> [--query k=v] [--payouts]` | any | GET-only escape hatch |

## Investigation

| Command | Question |
|---|---|
| `investigate payment <payment_id>` | Why did this payment fail? |
| `investigate order <order_id>` | They say they paid, our order says unpaid |
| `investigate refund <refund_id>` | What happened to this refund? |
| `investigate payout <payout_id>` | Where is this payout? |

## Per-domain help

Every group carries its own `usage` subcommand describing what it is for:

```
agent-dlocal usage
agent-dlocal payments usage
agent-dlocal investigate usage
```

## Environment

| Variable | Meaning |
|---|---|
| `AGENT_DLOCAL_PROFILE` | Default profile alias |
| `AGENT_DLOCAL_BASE_URL` | Base URL override (tests, mock) |
| `AGENT_DLOCAL_NO_KEYCHAIN` | Fall back to the 0600 index file |
| `DLOCAL_X_LOGIN` / `DLOCAL_X_TRANS_KEY` / `DLOCAL_SECRET_KEY` | Direct credentials, bypassing the keychain |
