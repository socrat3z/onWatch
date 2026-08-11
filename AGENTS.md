CLAUDE.md

## Test environment safety

- Tests must never read, write, delete, refresh, or migrate credentials, settings, databases, keychains, or CLI state from the developer's real user profile.
- Any test that exercises home-directory discovery must redirect all supported lookups. Use `internal/testenv.SetTestUserHome` or `internal/testenv.IsolateTestUserEnvironment`; setting `HOME` alone is not sufficient because Windows uses `USERPROFILE`.
- Package suites that can auto-detect local credentials must isolate the process in `TestMain` with `internal/testenv.IsolateProcessUserEnvironment` before running tests.
- Do not unset credential-path overrides when that would fall through to a real home directory. Point `CODEX_HOME`, `OPENCODE_HOME`, XDG paths, and similar overrides at empty temporary directories instead.
- Do not use `os.Clearenv` in tests. Clear only the application-owned variables required by the test and preserve host essentials such as `HOME`, `USERPROFILE`, `PATH`, and temporary-directory variables.
- Never use a real token, credential file, local database, CLI login, keychain, or user settings file as a test fixture. Create minimal synthetic data under `t.TempDir()`.
- Live credential, network, CLI, and keyring tests must be opt-in through an explicit environment flag, skip by default, and state clearly that they access external or user-owned state.
- Run Go and integration tests through `app.sh`; use `./app.sh --test` and `./app.sh --integration` rather than direct `go test` commands.
