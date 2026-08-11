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
// It also redirects every supported home/config lookup to a private temp root.
// Individual tests can replace those paths with their own temp roots, but must
// never clear them in a way that falls through to the real UserHomeDir path.
func TestMain(m *testing.M) {
	SetTestMode(true)
	cleanup, err := testenv.IsolateProcessUserEnvironment()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "isolate test user environment: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	testenv.SetTestUserHome(t, home)
}

func clearTestUserHome(t *testing.T) {
	t.Helper()
	testenv.ClearTestUserHome(t)
}
