# Simple Scheduled Quota Primer

**Date:** 2026-09-13  
**Status:** proposed  
**Goal:** send a tiny `"hi"` request to Codex or Claude at a user-selected time.

## Decision

Build this as a small, best-effort timer feature.

This is not a quota-management system. It does not need to inspect quota-window
state, guarantee exactly-once delivery, track every attempt, catch up missed
runs, or coordinate individual 5-hour and weekly buckets.

At the configured local time, onWatch sends `"hi"` to the selected provider.
That request may start any currently unstarted quota windows affected by a
normal request to that provider.

Duplicate requests are acceptable. If onWatch restarts around the scheduled
minute, it may send `"hi"` again.

## User-facing behavior

Settings for each supported provider:

- **Scheduled primer:** Off / On
- **Time:** local wall-clock time, such as `06:00`
- **Days:** every day, weekdays, or selected weekdays

The existing onWatch timezone setting determines what the selected time means.

The UI should say:

> Sends a tiny “hi” request at the selected time. This uses a small amount of
> quota and may start your provider's rolling quota windows. It does not add
> quota; it only influences when a window begins.

Show only two status values:

- Next scheduled run
- Last result: success or failure

Detailed history is unnecessary.

## Runtime design

Add one small application-owned scheduler, for example
`internal/primer/scheduler.go`.

The scheduler:

1. Wakes every 30 seconds.
2. Reads the current timezone and provider primer settings.
3. Checks whether the current local day and minute match a schedule.
4. Calls the provider sender when they match.
5. Remembers the fired provider/account/minute in memory so subsequent ticks in
   that minute do not fire continuously.

The in-memory marker can be discarded on restart. A restart during the matching
minute may cause another request, which is acceptable.

There is deliberately:

- no database ledger;
- no lease or distributed lock;
- no persisted rate limiter;
- no quota-state guard;
- no user-activity detection;
- no retry queue;
- no missed-run catch-up;
- no special DST engine beyond Go's normal timezone handling.

If the daemon or computer is not running at the scheduled time, that run is
missed. The next configured run happens normally.

Each send gets a short timeout. Failure is logged and shown as the last result;
the scheduler waits for the next scheduled occurrence rather than retrying.

## Provider adapters

### Codex

Reuse the existing Codex starter transport in
[`internal/api/codex_starter.go`](../../internal/api/codex_starter.go).

Change the minimal prompt to `"hi"`, or keep the existing tiny prompt if it is
already known to anchor the window reliably. Drain the streamed response before
returning, as the current implementation does.

Use each configured Codex account's existing client and account ID. A provider
schedule applies to all enabled accounts unless a later user request justifies
per-account schedules.

### Claude

Use the installed official Claude CLI rather than implementing a private OAuth
Messages request.

The adapter runs the smallest supported non-interactive command equivalent to:

```text
claude -p "hi"
```

Before implementation, manually confirm the exact flags for the supported
Claude CLI version and whether a low-cost model can be selected. Run with a
timeout and do not place credentials in command arguments or logs.

If the CLI is absent, not logged in, or exits unsuccessfully, record a normal
failure and wait for the next scheduled run. The primer must not implement its
own Anthropic token refresh.

Docker support may reuse the existing Claude credential lock where available.
Native installs simply rely on the official CLI's normal credential handling.

## Interaction with the existing Codex auto-starter

The current Codex feature reacts whenever it observes an unstarted 5-hour or
weekly window. That behavior can defeat a chosen schedule.

Keep backward compatibility with this simple rule:

- When the new scheduled Codex primer is **Off**, existing `auto_start_5h` and
  `auto_start_7d` settings continue to work as they do today.
- When the scheduled Codex primer is **On**, do not run either reactive
  auto-starter. The timer is the only starter policy.

Do not add separate schedules for the 5-hour and weekly windows. A normal
provider request may affect both, so the honest control is “send a request at
this time.”

## Implementation tasks

### 1. Scheduler

- Add the single ticker-based scheduler with an injectable clock for unit tests.
- Implement timezone, time-of-day, and weekday matching.
- Add the in-memory per-minute duplicate check.
- Start and stop it with the application context.

### 2. Settings and UI

- Add enabled, time, and days fields for Codex and Claude.
- Validate time as `HH:MM` and days against a small fixed vocabulary.
- Apply changes without a daemon restart.
- Show next run and last success/failure.
- Add the quota-cost and “does not add quota” disclosure.

### 3. Provider wiring

- Adapt the existing Codex starter sender to the scheduler.
- Add a narrowly scoped Claude command runner.
- Use a bounded context for both senders.
- Log provider, account, scheduled time, and success/failure without secrets or
  response bodies.

### 4. Tests

- Fires during the matching minute.
- Does not fire outside the matching minute or on an unselected day.
- Does not fire repeatedly on normal ticker ticks in the same minute.
- May fire again after a simulated process restart.
- Stops cleanly on context cancellation.
- Codex and Claude adapters use fake HTTP/command runners and synthetic
  credentials only.
- Run the suite through `./app.sh --test` as required by the repository.

## Out of scope

Do not add these unless real usage later demonstrates a need:

- exactly-once delivery across restarts;
- durable attempt history;
- retries or catch-up after sleep;
- quota-window detection before sending;
- separate 5-hour and weekly schedules;
- per-account schedules or staggered accounts;
- cost budgets and monthly cost reports;
- schedule recommendations based on usage history;
- raw Anthropic subscription-OAuth HTTP calls.

## Recommended delivery order

1. Add the disclosure and scheduled Codex controls.
2. Implement the small scheduler and reuse the existing Codex sender.
3. Add the Claude CLI adapter after confirming its minimal command manually.
4. Stop. Gather feedback before adding anything else.

## Success criteria

The feature is complete when a user can select Codex or Claude, choose a local
time and days, leave onWatch running, and observe a tiny request being sent at
that time.

Best-effort behavior is intentional. A few duplicate `"hi"` requests are not a
correctness problem.
