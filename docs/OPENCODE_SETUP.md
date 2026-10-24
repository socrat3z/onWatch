# OpenCode Go Setup Guide

Track OpenCode Go subscription quotas in onWatch.

OpenCode Go has no documented quota API. onWatch reads the subscription's own meters, and it can authenticate in either of two ways:

- **Go API key (recommended).** A key with usage read access. No browser cookie, and it does not expire when you log out. Key mode reads `GET /zen/go/v1/usage`, the same route the OpenCode CLI and console use.
- **Browser session.** Your workspace ID plus the `__Host-console_session` cookie from opencode.ai. Used only when no key is set. Session mode reads the console's `GET /console/api/go/status` document.

This is separate from `OPENCODE_ENABLED`, which only feeds ChatGPT credentials from OpenCode into the **Codex** provider.

> **Upgrading from the old scrape mode?** The `/workspace/{id}/go` page and the `auth` cookie no longer work ([issue #134](https://github.com/onllm-dev/onWatch/issues/134)). Keep your workspace ID, but replace `OPENCODE_GO_AUTH_COOKIE` with the value of the `__Host-console_session` cookie, or switch to a Go API key.

---

## What you see

Both sources return the subscription's three meters. onWatch shows them as the **5-Hour** (Rolling), **Weekly** and **Monthly** cards with the percentage OpenCode enforces and shows on the Go page.

- **5-Hour** is a rolling session window with its own reset time.
- **Weekly** resets at the time the API reports.
- **Monthly** renews with the subscription period; its reset is the period end the API reports, so no reset day needs to be configured. A plan without a monthly window simply omits that card.

Key mode reports an integer percentage per window; session mode reports micro-cents, which onWatch stores as USD. Charts and cycle history use the percentage either way. A key and a session for the same account show the same percentages.

onWatch reuses a response for 60 seconds, so a poll interval shorter than that does not add requests.

---

## Option 1: Go API key (recommended)

1. In the OpenCode console, open **Keys -> Add Service Account** (or your Go API key) and create a key with **usage read** access. Keys look like `oc_sk_...`.
2. Set it (or paste it into **Settings -> Providers -> OpenCode Go -> Usage API Key**):

   ```bash
   OPENCODE_GO_API_KEY=oc_sk_...
   ```

The meters belong to the Go subscription the key resolves to. To track a different subscription, use a key from that subscription's workspace.

The `onwatch setup` wizard offers **OpenCode Go only** (or add it under **Multiple** / **All available**). Paste a key to have it verified with one read-only request, or paste a `wrk_...` workspace ID to switch to session-cookie mode.

---

## Option 2: Browser session

### 1. Copy the session cookie

1. Sign in at https://opencode.ai and open the console
2. Open browser Developer Tools -> **Application** / **Storage** -> **Cookies** -> `https://opencode.ai`
3. Find the `__Host-console_session` cookie and copy its **value**

You can paste just the value, `__Host-console_session=<value>`, or a full `Cookie:` header copied from DevTools. Treat it like a password: logging out, rotating sessions, or clearing cookies invalidates it.

### 2. Find your workspace ID

With Developer Tools open on the **Network** tab, open your Go usage page in the console. Select the `status` request to `/console/api/go/status` and copy the value of its `x-org-id` request header. It looks like `wrk_...`.

### 3. Configure onWatch

Add both values to `~/.onwatch/.env` (or your project `.env`):

```bash
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_console_session_value
```

Both are required. Without them (and without `OPENCODE_GO_API_KEY`) the OpenCode Go provider stays disabled.

You can also set them in **Settings -> Providers -> OpenCode Go** (**Workspace ID** and **Session Cookie**).

---

## Reload / Restart

Saving a key or enable flag in **Settings -> Providers** applies live - onWatch starts (or restarts) the OpenCode Go agent without a daemon restart. A restart is still safe:

```bash
onwatch stop
onwatch
```

Or verify in the foreground with `onwatch --debug`. You should see `OpenCode poll complete` with `quota_count=3`.

Then open http://localhost:9211, switch to the **OpenCode** tab, and confirm the 5-Hour, Weekly and Monthly cards populate. Charts, cycle overview and insights fill in after a few polls. The macOS menubar shows the OpenCode Go card once a poll has produced data.

---

## Security Notes

- Never commit `.env` or paste the key or cookie into issue reports / logs
- onWatch redacts `api_key` and `auth_cookie` from `/api/settings` responses
- Response bodies are never written to logs or echoed in errors (they contain account IDs)
- The session cookie is only sent to `opencode.ai`, and redirects are never followed
- All processing stays local on your machine

---

## Limitations & Notes

- The usage routes are undocumented. If OpenCode changes their shape, onWatch reports a parse failure rather than showing wrong numbers.
- Session cookie lifetime is controlled by OpenCode. Expect to refresh it after logout or session rotation; a Go API key avoids this.
- The workspace ID is required in session mode; onWatch does not auto-discover workspaces.
- The console usage export (`/console/api/v1/usage/export`) is not used: its Go rows carry no cost, and it answers 403 to service-account keys. See [anomalyco/opencode#50912](https://github.com/anomalyco/opencode/issues/50912).

---

## Troubleshooting

### No OpenCode tab

- Confirm `OPENCODE_GO_API_KEY` is set, or both `OPENCODE_GO_WORKSPACE_ID` and `OPENCODE_GO_AUTH_COOKIE`
- Check the log for `OpenCode Go not configured; skipping polling` (no credential) or `provider enabled but not configured` (enabled with no key). Both name the setting to fix.

### Unauthorized / forbidden

- **Key:** check the Go API key still exists and has usage read access. The log names the setting: `check the OpenCode Go Usage API Key in Settings`.
- **Session:** the log says `paste the __Host-console_session cookie`. Re-copy a fresh `__Host-console_session` value while signed in; the old `auth` cookie is rejected. A `400` / invalid response usually means the workspace ID is wrong.
- A 429 means the API is rate limiting; onWatch skips that poll and tries again on the next one.

### Parse failed / response format changed

OpenCode likely changed the usage response. File an issue with:

- Approximate time of failure
- Whether the browser console still shows 5h / weekly / monthly usage
- **Do not** attach keys, cookies or raw responses (they contain account IDs)

### Docker / headless

Pass the env vars into the container. There is no local credential auto-detection for OpenCode Go.

```bash
OPENCODE_GO_API_KEY=oc_sk_...
# or, browser session:
OPENCODE_GO_WORKSPACE_ID=wrk_xxxxxxxx
OPENCODE_GO_AUTH_COOKIE=your_console_session_value
```

---

## Related

- Main README environment variable reference
- Codex + OpenCode ChatGPT auth (`OPENCODE_ENABLED`) is documented in [CODEX_SETUP.md](CODEX_SETUP.md)
