package api

import (
	"fmt"
	"os"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/testenv"
)

// TestMain runs before all tests in the api package. It enables test mode
// to prevent tests from reading or writing real credentials in the macOS
// Keychain or Linux keyring. Without this, tests that call
// WriteAnthropicCredentials or DetectAnthropicToken can overwrite the user's
// real Claude Code OAuth tokens, causing Claude Code to be logged out.
//
// It also points the home directory at an empty sandbox for the whole run and
// clears provider location overrides (CODEX_HOME, OPENCODE_HOME, XDG_*, ...),
// so a test that forgets to isolate itself can never read or write the real
// ~/.claude, ~/.codex, ~/.kimi-code and so on. Individual tests that must stay
// hermetic should call isolateOpenCodeEnv (pins OPENCODE_HOME/XDG_DATA_HOME to
// empty temp dirs) because clearing alone can still fall through to the
// UserHomeDir path.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	// SetTestMode must run before HOME/USERPROFILE are redirected: its first
	// enable records the real home that the credential-file guard refuses.
	SetTestMode(true)

	cleanup, err := testenv.IsolateProcessUserEnvironment()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "isolate test user environment: %v\n", err)
		return 1
	}
	defer cleanup()

	return m.Run()
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	testenv.SetTestUserHome(t, home)
}

func clearTestUserHome(t *testing.T) {
	t.Helper()
	testenv.ClearTestUserHome(t)
}
