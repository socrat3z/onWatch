# Downstream Fork Architecture Guidelines & Clean-Merge Criteria

## Executive Summary
This document establishes the architectural standards, design patterns, and evaluation criteria for maintaining this downstream fork (`onWatch` fork-overlay). Its goal is to allow the repository to continuously incorporate upstream changes (`onllm-dev/onWatch`) with near-zero merge conflicts, high visibility of fork deltas, and minimal maintenance overhead.

---

## 1. The Downstream Fork Dilemma

In long-lived software forks, the primary risk is **divergence entropy**:
- Every line modified directly inside an upstream file creates a collision surface.
- Upstream refactors or bug fixes in those files result in three-way merge conflicts (`UU`).
- Over months, resolving these conflicts manually leads to accidental regression of upstream fixes or silent corruption of fork features.

To eliminate divergence entropy, the repository must transition from **invasive patching** (editing upstream files in-place) to an **overlay & extension architecture** (wrapping upstream code via well-defined boundaries).

---

## 2. Core Architectural Principles

### Principle 1: Additive Over Modifying (Open-Closed Principle for Forks)
- **Rule**: Upstream files are *closed for modification*, *open for extension*.
- Additive files (new files in existing directories or dedicated subpackages like `internal/account/`) merge with upstream with **0% conflict probability**. Git never conflicts on new files added solely in a fork.
- Any new struct, handler, helper, or test should be placed in an additive file rather than appended to an existing upstream file.

#### The Function Placement Invariant (What Belongs Where)
To maintain a predictable codebase and avoid catastrophic merge conflicts:
1. **Only NEW functions and overlay-specific types belong in new files**:
   - Entirely new functions, new types, new HTTP handlers, and overlay registration hooks belong in `*_overlay.go`, `fork_overlay.js`, `fork_overlay.css`, or dedicated packages (`internal/account/`).
2. **Existing upstream functions with additions MUST remain in their original main files**:
   - If a function already existed in upstream and receives additions, extensions, or parameter adaptations for fork features, **it must remain in the upstream file**.
   - **Anti-Pattern (PROHIBITED)**: Moving an existing upstream function into an `*_overlay.go` file to artificially minimize the diff of the upstream file. This destroys `git blame`, causes three-way merge conflicts (`delete/modify` collisions) on every future upstream pull, and scatters standard package behavior.
   - **Proper Pattern**: Keep the upstream function in place. If the fork-specific logic within it is substantial, extract *only the new helper function* into an overlay file and invoke it from the original upstream function.

### Principle 2: Minimal-Touch Boundary (Single-Line Hooks)
- When upstream execution flow must invoke fork functionality, the intrusion into upstream structural files must be reduced to a **minimal hook point** (ideally 1 to 3 lines):
  ```go
  // GOOD: Single-line hook in upstream server.go
  registerForkOverlayRoutes(mux, s.store, s.logger)
  ```
  ```go
  // BAD: 80 lines of custom route handlers and switch cases pasted directly into upstream handlers.go
  ```
- Note: Minimal-touch boundaries apply to **structural integration points** (server route registration, startup hooks, schema runner dispatch). They do not mandate moving existing upstream business logic functions into overlay files.
### Principle 3: Pluggable Registries Over Monolithic Switches
- Upstream dispatchers often use hardcoded `switch` statements (e.g., matching CLI commands, provider names, or HTTP routes).
- Do not add new `case` branches directly inside upstream switches.
- Instead, introduce a delegate or registry lookup:
  ```go
  if handled, err := overlay.HandleSubcommand(action, args); handled {
      return err
  }
  ```
- **Current HTTP boundary**: `internal/web/server.go` calls
  `registerForkOverlayRoutes` once. All downstream routes are registered in
  `internal/web/server_overlay.go`. Fork-only account response/session helpers
  live in `internal/web/current_accounts_overlay.go` rather than the monolithic
  upstream handler file.

### Principle 4: Decoupled Database Migrations (Isolated DDL Sequences)
- **Do not interleave fork schema changes into upstream's linear `migrateSchema()` version numbers.**
- If upstream is at schema version 8 and the fork introduces version 9, upstream's future release of version 9 will collide destructively.
- **Pattern**:
  1. Let upstream run its `migrateSchema(db)` untouched.
  2. Execute a secondary fork migration runner: `migrateForkOverlaySchema(db)` using either independent version tracking (`fork_schema_version`) or strictly idempotent `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE ... ADD COLUMN` statements with column existence checks.
- **Current boundary**: `internal/store/store.go` contains only the post-upstream
  invocation. Fork DDL, indexes, and data backfills live in
  `internal/store/fork_schema_overlay.go`; its boundary test verifies that the
  upstream runner does not apply fork columns and that the overlay is idempotent.

### Principle 5: Overlay Asset Architecture (CSS & JavaScript)
- Monolithic frontend files (`app.js`, `style.css`, `dashboard.html`) are the highest-conflict files in web applications.
- **Rule**: Do not insert hundreds of lines into `app.js` or `style.css`.
- **Pattern**:
  - Fork JavaScript lives in `internal/web/static/fork_overlay.js`.
  - Fork CSS lives in `internal/web/static/fork_overlay.css`.
  - Upstream templates include the overlay via a single link/script tag:
    ```html
    <link rel="stylesheet" href="/static/fork_overlay.css">
    <script defer src="/static/fork_overlay.js"></script>
    ```
  - `fork_overlay.js` uses standard DOM event delegation (`document.addEventListener('DOMContentLoaded', ...)`), mutation observers, or window hooks to enhance upstream UI components non-invasively.

### Principle 6: Facade & Decorator Pattern for Core Components
- When fork logic needs to enhance an upstream agent, store, or client:
  - Do not edit the upstream struct definition or methods.
  - Wrap the upstream struct inside a fork manager or decorator struct:
    ```go
    type MultiAccountAgentManager struct {
        provider string
        agents   map[int64]*upstream.AnthropicAgent
    }
    ```
  - This allows upstream to freely refactor `AnthropicAgent` internals without breaking the outer manager.
- **Current startup boundary**: `cmd/onwatch/main_overlay.go` coordinates all
  fork account managers. The upstream startup path has one hook each for
  construction, notifier wiring, and agent registration.

### Principle 7: Hermetic & Portable Testing
- Tests added by the fork must respect the strict isolation rules:
  - Redirect all home directory lookups (`HOME`, `USERPROFILE`, `LOCALAPPDATA`).
  - Bind exclusively to `127.0.0.1` (never wildcard `0.0.0.0` or empty host which trigger OS firewall prompts).
  - Use graceful child process termination and wait routines to release file locks on Windows and Unix alike.

---

## 3. The 10 Fork PR & Refactoring Evaluation Criteria

When reviewing changes or designing new fork features, evaluate against these criteria:

| # | Criterion | Target | Verification Check |
|---|---|---|---|
| **C1** | **Structural Touchpoint Minimization** | $\le 5$ touched lines per structural file | Structural files (`server.go`, `main.go`, `store.go`) contain only hook invocations. |
| **C2** | **Upstream Function Integrity** | 100% upstream functions stay in place | Zero functions from `upstream/main` relocated to `*_overlay.go` files. |
| **C3** | **Additive File Segregation** | $> 90\%$ of new code in dedicated files | Genuinely new code lives in `*_overlay.go`, `*_ext.go`, or dedicated packages (`internal/account/`). |
| **C4** | **Isolated Schema Migrations** | 0 interleaved versions in `migrateSchema` | Fork migrations execute in `migrateForkSchema` with idempotent guards. |
| **C5** | **Decoupled Frontend Assets** | No monolithic script/style bloat | Custom UI logic and styling isolated in `fork_overlay.js` and `fork_overlay.css`. |
| **C6** | **Pluggable Command/Route Registry** | Single delegate entry point | Subcommands and custom HTTP endpoints registered via delegate hooks. |
| **C7** | **Non-Invasive Config Extension** | Clean separation of fork config | Fork configuration options grouped cleanly or parsed via overlay helpers. |
| **C8** | **Hermetic Test Isolation** | Zero developer profile leakage | Tests pass on Linux, macOS, and Windows with `SetTestUserHome`. |
| **C9** | **Zero Firewall Intrusiveness** | 100% loopback binding | All test listeners and mock servers bind explicitly to `127.0.0.1`. |
| **C10**| **Documented Fork Delta** | 100% features documented | Every added capability described in `docs/FORK_FEATURES.md`. |
| **C11**| **Automated Verification Gate** | Clean pass in $< 35\text{s}$ | `./app.sh --smoke` passes cleanly without timeouts or flakiness. |

---

## 4. Upstream Alignment & Merging Protocol

To synchronize this repository with `upstream/main`, follow this standard procedure:

```bash
# 1. Fetch latest upstream changes
git fetch upstream main

# 2. Inspect incoming changes against the merge base
git log --oneline --graph HEAD...upstream/main

# 3. Check which files overlap between upstream and fork changes
comm -12 <(git diff --name-only $(git merge-base HEAD upstream/main)..upstream/main | sort) \
         <(git diff --name-only $(git merge-base HEAD upstream/main)..HEAD | sort)

# 4. Perform the merge
git merge upstream/main

# 5. Resolve conflicts respecting the Function Placement Invariant
#    - If a conflict occurs within an existing upstream function, resolve it directly in the upstream file.
#    - NEVER delete or move an upstream function into an overlay file during conflict resolution.
#    - If an upstream file conflicted on brand-new fork functions, move ONLY those new functions into an overlay file.

# 6. Verify immediately with the smoke suite
./app.sh --smoke

# 7. Commit with clear standard message
git commit -m "chore(upstream): merge upstream/main into fork-overlay"
```

---

## 5. Directory & File Naming Conventions

To instantly distinguish upstream code from fork extensions:
- **`internal/account/`**: Multi-account domain models, account detection, and storage.
- **`*_overlay.go`**: Extensions to upstream packages that provide hook registration, new types, or new facade wrappers.
  - **Strict Constraint**: Must contain **ONLY new functions and types**. Never move an existing upstream function here.
- **`*_overlay_test.go`**: Unit tests verifying the overlay hook points without mocking upstream internals.
- **`internal/web/static/fork_overlay.js`**: Frontend JavaScript enhancements (DOM observers, account pickers, freshness banners).
- **`internal/web/static/fork_overlay.css`**: Frontend CSS rules for fork UI elements.
- **`docs/FORK_*.md`**: Fork architectural documentation and feature specifications.
