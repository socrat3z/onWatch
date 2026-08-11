package agent

import (
	"fmt"
	"os"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/testenv"
)

// TestMain prevents any agent test from falling back to credentials, keyrings,
// or settings in the developer's real user profile.
func TestMain(m *testing.M) {
	api.SetTestMode(true)
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
