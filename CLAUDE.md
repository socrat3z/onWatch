# onWatch

Go CLI for AI quota tracking. Polls 15 providers → SQLite → Material Design 3 dashboard.

## Task

Background daemon (<50MB RAM) tracking: Anthropic, Synthetic, Z.ai, Copilot, Codex, MiniMax, Antigravity, Gemini, Cursor, Kimi Code, Grok, Moonshot, DeepSeek, OpenRouter, OpenCode Go.

## Code Map

```
main.go                     # CLI entry, daemon lifecycle
internal/
├── api/                    # HTTP clients + types per provider
│   └── {provider}_client.go, {provider}_types.go
├── store/                  # SQLite persistence per provider
│   └── store.go (schema), {provider}_store.go
├── tracker/                # Poll orchestration per provider
├── agent/                  # Background polling agents
├── web/                    # Dashboard server
│   ├── handlers.go         # API endpoints
│   ├── static/             # Embedded JS/CSS (embed.FS)
│   └── templates/          # HTML templates
├── config/                 # Config + container detection
└── notify/                 # Email + push notifications
```

## Objectives

1. **TDD-first**: Test → fail → implement → pass
2. **RAM-bounded**: 40MB limit, single SQLite conn, lean HTTP
3. **Single binary**: All assets via `embed.FS`

## Operations

**Always use `app.sh` for build and test - never run `go build` or `go test` directly.**

```bash
./app.sh --build            # Build production binary
./app.sh --test             # Run all tests with race detection and coverage
./app.sh --integration      # Run integration-tagged tests with race detection
./app.sh --smoke            # Quick validation: vet + build check + short tests
go test -race ./... && go vet ./...   # Pre-commit (mandatory)
```

**Nix (alternative to app.sh, for build + dev shell):**

```bash
nix build .#onwatch         # Pure-Go static binary -> ./result/bin/onwatch
nix run .#onwatch           # Build + run
nix develop                 # Shell with go/gopls/gotools/gofumpt (or `direnv allow`)
```

On `go.sum` changes, update `vendorHash` in `flake.nix` (run `nix build .#onwatch`, paste the `got: sha256-...` from the error). Flake tracks `nixos-unstable` because `go.mod` requires Go >= 1.25.7.

## Guardrails

| Rule | Reason |
|------|--------|
| Never commit `.env`, `.db`, binaries | Security |
| Never log API keys | Security |
| Parameterized SQL only | Injection prevention |
| `context.Context` always | Leak prevention |
| `-race` before commit | Data race detection |
| `subtle.ConstantTimeCompare` for creds | Timing attacks |
| Bounded queries (cycles≤200, insights≤50) | Memory caps |

## Notes

**Adding a provider:**
1. `internal/api/{provider}_client.go` + `_types.go`
2. `internal/store/{provider}_store.go`
3. `internal/tracker/{provider}_tracker.go`
4. `internal/agent/{provider}_agent.go`
5. Add to `internal/web/handlers.go` endpoints
6. Update dashboard JS in `internal/web/static/app.js`

**API Docs:** See `docs/` for provider-specific setup (MULTI_ACCOUNT.md for multiple accounts per provider, WITH_USER_ENV.md for the fork container image, COPILOT_SETUP.md, CODEX_SETUP.md, ANTIGRAVITY_SETUP.md, GEMINI_SETUP.md, CURSOR_SETUP.md, KIMI_SETUP.md, GROK_SETUP.md, MOONSHOT_SETUP.md, DEEPSEEK_SETUP.md, OPENCODE_SETUP.md)

**Containers:** `IsDockerEnvironment()` in `config.go` detects Docker/K8s. Containers run foreground only.

**with-user-env image (fork-only):** `Dockerfile.with-user-env` + `docker-compose.override-with-user-env.yml` + `docker-entrypoint-with-user-env.sh` add the `agy`, `codex`, and `claude` CLIs for providers that need an interactive OAuth login. Rules when touching it:
- The CLIs exist only to run a login, through the isolated one-shot `agy-login`/`codex-login`/`claude-login` services (`docker compose --profile login run --rm <svc>`). Those services get one credential volume each and deliberately no `env_file` and no `/data`. Polling reads the token files (`~/.gemini`, `~/.codex/auth.json`, `~/.claude/.credentials.json`), never the CLIs.
- The entrypoint allowlists exact login commands and exits 64 for anything else. Changing the allowlist means changing `scripts/test-entrypoint-dispatch.sh` in the same commit.
- Codex and Claude Code are pinned by version AND SHA-256 in the `cli-installer` stage, fetched as npm release tarballs and reduced to one native binary each. No package manager, install script, or lifecycle hook in that stage; Bun/Node/npm must never come back. (`agy` is the exception and is fetched by its unpinnable bootstrapper in the `runtime` stage - see the comment there.) `TARGETARCH` maps to an exact upstream path - no `find | head -1`. Keep `codex-code-mode-host` pruned. Bumping a version follows `docs/WITH_USER_ENV.md#updating-the-pinned-clis`.
- One named volume per credential store, nothing else mounted. Adding a volume or a CLI needs a matching reason in `docs/WITH_USER_ENV.md`.
- Daemon memory is 512M because `ANTIGRAVITY_SOURCE=cli` makes the daemon spawn the 190M `agy` binary and keep it warm; idle is 9.4 MiB. The 512M on the `*-login` services is an unmeasured TUI ceiling. Measurements and their date live in `docs/WITH_USER_ENV.md#measured-resource-budget` - update them there when bumping a pinned CLI.

**Release process (all 3 steps required):**
1. Update version in all 3 places:
   - `VERSION` file (the single source of truth, e.g. `2.11.42`)
   - `README.md` version badge text (`Version-v2.11.42`)
   - `README.md` version badge link (`/releases/tag/v2.11.42`)
2. Commit, push to main, and create a git tag (`git tag v2.11.42 && git push origin main --tags`)
3. Trigger the GitHub Actions release workflow: `gh workflow run release.yml -f tag=v2.11.42`
   - Do NOT use `gh release create` - the workflow handles release creation, cross-compilation (5 platforms), and binary uploads
   - Do NOT run `./app.sh --release` locally - the release must happen through the GitHub Actions pipeline

**Anthropic Rate Limit Bypass:** Anthropic's usage API has aggressive rate limits (~5 requests per token, then 429 for ~5 min). onWatch bypasses this by refreshing the OAuth token when rate limited - each new access token gets a fresh rate limit window. Implementation details:
- `internal/agent/anthropic_agent.go`: Detects 429, calls `RefreshAnthropicToken`, saves new tokens, retries
- `internal/api/anthropic_oauth.go`: OAuth token refresh endpoint (`console.anthropic.com/v1/oauth/token`)
- `internal/api/anthropic_token_unix.go`: Writes to macOS Keychain + file for persistence
- `internal/api/anthropic_token_windows.go`: Writes to credentials file
- Refresh tokens are one-time use (OAuth rotation) - MUST save new refresh token after each refresh
- See: [issue #16](https://github.com/onllm-dev/onWatch/issues/16), [anthropics/claude-code#31021](https://github.com/anthropics/claude-code/issues/31021)

## Style

- Use `-` (hyphen) instead of `—` (em dash) in all text
