# HTTP API

## Provider account aliases

For discovered Anthropic and Antigravity accounts, use `GET /api/accounts?provider=anthropic`
or `GET /api/accounts?provider=antigravity` to list account IDs, credential-folder
names, display aliases, and deletion state. Update only the display label with:

```http
PATCH /api/accounts?provider=anthropic
Content-Type: application/json

{"account_id": 12, "alias": "Acme work"}
```

Aliases are non-secret metadata. They never move credential files or restore a
deleted account, which keeps account discovery and historical telemetry safe.
See [Multiple Accounts Per Provider](MULTI_ACCOUNT.md) for how accounts are
discovered and what `&account=<id>` does on the read endpoints.

onWatch serves the same authenticated `/api/*` endpoints its dashboard uses, so
anything the dashboard shows can be scripted. The full endpoint list lives in
the [README](../README.md#api-endpoints); this document covers the read
endpoints in detail, with response shapes for the balance-based providers
(DeepSeek, Moonshot, OpenRouter) whose payloads differ from the quota providers.

## Authentication

All `/api/*` routes accept dashboard Basic Auth. Replace the URL and
credentials below with the values used by your onWatch instance:

```bash
export ONWATCH_URL="http://localhost:9211"
export ONWATCH_USER="admin"
export ONWATCH_PASS="changeme"
```

If onWatch is configured with a base path, include it in `ONWATCH_URL`, for
example `https://example.com/onwatch`.

## Read endpoints

| Endpoint | Description |
|---|---|
| `/api/current?provider=<name>` | Latest stored snapshot for one provider |
| `/api/current?provider=both` | Every configured, dashboard-visible provider in one payload |
| `/api/history?provider=<name>&range=<range>` | Chart-ready history; ranges include `6h`, `24h`, `7d`, `30d` |
| `/api/summary?provider=<name>` | Usage summary (rate, projections, cycle aggregates) |
| `/api/insights?provider=<name>&range=<range>` | Rendered insight cards |
| `/api/cycles?provider=<name>&type=<quota>` | Reset cycle history |

`<name>` is any configured provider key: `anthropic`, `synthetic`, `zai`,
`copilot`, `codex`, `minimax`, `antigravity`, `gemini`, `cursor`, `kimi`,
`grok`, `moonshot`, `deepseek`, `openrouter`, `opencode`, or `both`.

Quota providers return a `quotas` array. The balance providers documented below
return a `balance` or `credits` object instead, because they track remaining
credit rather than a consumed allowance.

### Account-scoped providers in the combined payload

Anthropic, Antigravity, Codex, and MiniMax can each hold several accounts. In
`?provider=both` they follow one rule:

| Visible accounts | Key | Value |
|---|---|---|
| 1 | `<provider>` | the account's payload |
| 2 or more | `<provider>Accounts` | an array of those payloads |
| 0 (all switched off in settings) | - | the provider is omitted |

The two keys are mutually exclusive, so a single-account install keeps the flat
shape. Every payload carries the account it describes:

| Field | Meaning |
|---|---|
| `accountId`, `id` | the account's durable ID - what `&account=` and the `<provider>:<id>` visibility settings key take |
| `name` | the credential folder name, which is never renamed |
| `accountName` | the display alias, and the only field meant for rendering |

Anthropic and Antigravity payloads additionally carry a nested `account` object
with `alias`, `isDefault`, `accountCount`, and credential `health` - the same
shape `/api/accounts` returns.

## DeepSeek current balance

```bash
curl --fail --silent --show-error \
  --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=deepseek"
```

Example response:

```json
{
  "capturedAt": "2026-08-07T12:00:00Z",
  "snapshotAvailable": true,
  "balance": {
    "name": "Balance",
    "description": "DeepSeek API balance",
    "snapshotAvailable": true,
    "available": true,
    "currency": "USD",
    "total": 18.42,
    "granted": 3.42,
    "toppedUp": 15,
    "rate": 0.12,
    "status": "healthy"
  }
}
```

Relevant fields:

| JSON path | Type | Meaning |
|---|---:|---|
| `capturedAt` | string or null | UTC timestamp of the stored provider snapshot; `null` when none exists |
| `snapshotAvailable` | boolean | Whether a poll result is stored; see [No data yet](#no-data-yet) |
| `balance.snapshotAvailable` | boolean | Same signal, mirrored inside the balance object |
| `balance.available` | boolean or null | Whether DeepSeek reports sufficient balance for API calls; `null` without a snapshot |
| `balance.currency` | string | Currency reported by DeepSeek, normally `USD` or `CNY` |
| `balance.total` | number | Current spendable balance |
| `balance.granted` | number | Unexpired promotional or granted balance |
| `balance.toppedUp` | number | Purchased top-up balance |
| `balance.rate` | number or null | Estimated balance consumption per hour from onWatch history |
| `balance.status` | string | `healthy`, `exhausted`, or `unknown` |

Retrieve only the current total:

```bash
curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=deepseek" | jq '.balance.total'
```

DeepSeek source endpoint: [`GET /user/balance`](https://api-docs.deepseek.com/api/get-user-balance/).

## Moonshot current balance

```bash
curl --fail --silent --show-error \
  --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=moonshot"
```

Example response:

```json
{
  "capturedAt": "2026-08-07T12:00:00Z",
  "snapshotAvailable": true,
  "balance": {
    "name": "Balance",
    "description": "Moonshot Kimi API balance",
    "snapshotAvailable": true,
    "available": 12.5,
    "voucher": 2.5,
    "cash": 10,
    "rate": 0.08,
    "status": "healthy"
  }
}
```

| JSON path | Type | Meaning |
|---|---:|---|
| `capturedAt` | string or null | UTC timestamp of the stored provider snapshot; `null` when none exists |
| `snapshotAvailable` | boolean | Whether a poll result is stored; see [No data yet](#no-data-yet) |
| `balance.snapshotAvailable` | boolean | Same signal, mirrored inside the balance object |
| `balance.available` | number or null | Total spendable balance - an amount here, not a flag |
| `balance.voucher` | number or null | Promotional or granted voucher credits |
| `balance.cash` | number or null | Purchased cash balance |
| `balance.rate` | number or null | Estimated balance consumption per hour from onWatch history |
| `balance.status` | string | `healthy`, `exhausted`, or `unknown` |

Note that Moonshot's `balance.available` is an amount, whereas DeepSeek's
`balance.available` is a boolean flag. Moonshot source endpoint:
`GET https://api.moonshot.ai/v1/users/me/balance`.

## OpenRouter current balance

```bash
curl --fail --silent --show-error \
  --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=openrouter"
```

Example response when `OPENROUTER_API_KEY` is a management key:

```json
{
  "capturedAt": "2026-08-07T12:00:00Z",
  "snapshotAvailable": true,
  "credits": {
    "name": "Credits",
    "description": "OpenRouter API credits usage",
    "snapshotAvailable": true,
    "balance": 74.75,
    "totalCredits": 100.5,
    "accountUsage": 25.75,
    "balanceAvailable": true,
    "usage": 12.5,
    "usageDaily": 1.25,
    "limit": 20,
    "keyLimitRemaining": 7.5,
    "remaining": 7.5,
    "percent": 62.5,
    "isFreeTier": false
  }
}
```

Account-credit fields and API-key fields are intentionally separate:

| JSON path | Type | Meaning |
|---|---:|---|
| `snapshotAvailable` | boolean | Whether a poll result is stored; see [No data yet](#no-data-yet) |
| `credits.snapshotAvailable` | boolean | Same signal, mirrored inside the credits object |
| `credits.balance` | number or null | Actual account balance: `totalCredits - accountUsage` |
| `credits.totalCredits` | number or null | Total credits purchased for the account |
| `credits.accountUsage` | number or null | Total usage charged against account credits |
| `credits.balanceAvailable` | boolean | Whether account-wide credit data was available during the poll |
| `credits.usage` | number | Usage attributed to the configured API key |
| `credits.usageDaily` | number | Current-day usage attributed to the API key |
| `credits.limit` | number or null | Optional spending limit configured on the API key |
| `credits.keyLimitRemaining` | number or null | Remaining amount under that API-key limit |
| `credits.remaining` | number or null | Compatibility alias of `keyLimitRemaining`; it is not account balance |
| `credits.percent` | number | Percentage of the API-key limit consumed; `0` without a limit |

Retrieve only the account balance and reject unavailable data:

```bash
curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=openrouter" |
  jq -e 'select(.credits.balanceAvailable) | .credits.balance'
```

OpenRouter's account-credit endpoint requires a management key. With a normal
inference key, onWatch continues collecting key usage, but returns:

```json
{
  "balance": null,
  "totalCredits": null,
  "accountUsage": null,
  "balanceAvailable": false
}
```

Do not fall back to `credits.remaining` when you require the account balance;
that field represents only the configured API-key spending cap.

OpenRouter source endpoints:

- [`GET /api/v1/credits`](https://openrouter.ai/docs/api/api-reference/credits/get-credits) for account credits
- [`GET /api/v1/key`](https://openrouter.ai/docs/api/api-reference/api-keys/get-current-key) for key usage and limits

## No data yet

Before the first successful poll - or when a provider is configured but has
never returned a usable response - onWatch has no snapshot to report. Rather
than inventing a zero balance, the current endpoint returns
`snapshotAvailable: false`, a null `capturedAt`, null amounts, and the balance
status `unknown`. This applies to all three balance providers and is carried
through the aggregated `?provider=both` payload. The dashboard renders `--` and
a neutral "No data" badge in that state.

```json
{
  "capturedAt": null,
  "snapshotAvailable": false,
  "balance": {
    "name": "Balance",
    "description": "DeepSeek API balance",
    "snapshotAvailable": false,
    "status": "unknown",
    "available": null,
    "currency": "",
    "total": null,
    "granted": null,
    "toppedUp": null,
    "rate": null
  }
}
```

Gate on the signal before treating a balance as real:

```bash
curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=deepseek" |
  jq -e 'select(.snapshotAvailable) | .balance.total'
```

A stored snapshot reporting `0` is different: `snapshotAvailable` stays `true`
and the status becomes `exhausted`, because the account really is empty.

## Historical balance data

Use the history endpoint with one of the supported ranges such as `6h`, `24h`,
`7d`, or `30d`:

```bash
curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/history?provider=deepseek&range=7d"

curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/history?provider=openrouter&range=7d"
```

History returns an array of rows, each with `capturedAt` plus the
provider's amounts. Rows exist only for polls that succeeded, so an empty array
means the same thing as `snapshotAvailable: false` above.

| Provider | Row fields |
|---|---|
| `deepseek` | `total_balance`, `granted_balance`, `topped_up_balance`, `currency`, `available` |
| `moonshot` | `available_balance`, `voucher_balance`, `cash_balance` |
| `openrouter` | `balance`, `totalCredits`, `accountUsage` when account-credit retrieval succeeded, plus the key-level `usage`, `usageDaily`, and optional `percent` |

## Retrieve every provider in one request

```bash
curl --fail --silent --user "$ONWATCH_USER:$ONWATCH_PASS" \
  "$ONWATCH_URL/api/current?provider=both" |
  jq '{deepseek: .deepseek.balance, moonshot: .moonshot.balance, openrouter: .openrouter.credits}'
```

Only configured and dashboard-visible providers are included in the `both`
response, so check for the key before reading it.
