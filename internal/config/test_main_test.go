package config

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/onllm-dev/onwatch/v2/internal/testenv"
)

var configEnvPrefixes = []string{
	"ONWATCH_", "SYNTRACK_", "SYNTHETIC_", "ZAI_", "ANTHROPIC_",
	"COPILOT_", "CODEX_", "OPENCODE_", "ANTIGRAVITY_", "MINIMAX_",
	"OPENROUTER_", "MOONSHOT_", "DEEPSEEK_", "GEMINI_", "CURSOR_",
	"GROK_", "KIMI_",
}

var configEnvNames = map[string]struct{}{
	"DATABASE_URL":            {},
	"DOCKER_CONTAINER":        {},
	"KUBERNETES_SERVICE_HOST": {},
}

// TestMain removes only configuration inputs owned by onWatch. It deliberately
// preserves HOME, USERPROFILE, PATH, TEMP, and other host-process essentials.
func TestMain(m *testing.M) {
	cleanup, err := testenv.IsolateProcessUserEnvironment()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "isolate test user environment: %v\n", err)
		os.Exit(1)
	}
	clearConfigTestEnv()
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func clearConfigTestEnv() {
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := configEnvNames[key]; ok {
			_ = os.Unsetenv(key)
			continue
		}
		for _, prefix := range configEnvPrefixes {
			if strings.HasPrefix(key, prefix) {
				_ = os.Unsetenv(key)
				break
			}
		}
	}
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	testenv.SetTestUserHome(t, home)
}
