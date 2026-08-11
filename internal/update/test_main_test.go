package update

import (
	"fmt"
	"os"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/testenv"
)

func TestMain(m *testing.M) {
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
