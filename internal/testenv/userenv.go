package testenv

import (
	"fmt"
	"os"
	"path/filepath"
)

// EnvTestingT is the subset of testing.T used by the user-environment helpers.
type EnvTestingT interface {
	Helper()
	Setenv(key, value string)
	TempDir() string
}

// ClearTestUserHome removes every home-directory fallback used by Go. Use it
// only in tests that explicitly exercise the no-home error path.
func ClearTestUserHome(t EnvTestingT) {
	t.Helper()
	for _, key := range []string{
		"HOME", "USERPROFILE", "HOMEDRIVE", "HOMEPATH",
		"APPDATA", "LOCALAPPDATA", "XDG_CONFIG_HOME", "XDG_DATA_HOME",
		"XDG_CACHE_HOME", "XDG_STATE_HOME", "CODEX_HOME", "OPENCODE_HOME",
	} {
		t.Setenv(key, "")
	}
}

// SetTestUserHome redirects common cross-platform home and user-data lookups to
// home. Tests that exercise local configuration or credentials must use this
// instead of setting HOME alone: os.UserHomeDir reads USERPROFILE on Windows.
func SetTestUserHome(t EnvTestingT, home string) {
	t.Helper()
	paths := isolatedUserEnvironment(home)
	for key, value := range paths {
		t.Setenv(key, value)
	}
}

// IsolateTestUserEnvironment creates and installs a private user environment.
func IsolateTestUserEnvironment(t EnvTestingT) string {
	t.Helper()
	home := t.TempDir()
	SetTestUserHome(t, home)
	return home
}

// IsolateProcessUserEnvironment installs a private user environment for a
// package TestMain. The returned cleanup must be called before os.Exit.
func IsolateProcessUserEnvironment() (func(), error) {
	home, err := os.MkdirTemp("", "onwatch-test-user-*")
	if err != nil {
		return nil, fmt.Errorf("create isolated test user environment: %w", err)
	}

	paths := isolatedUserEnvironment(home)
	for key, value := range isolatedCredentialPaths(home) {
		paths[key] = value
	}
	for key, value := range isolatedCredentialEnvironment() {
		paths[key] = value
	}
	type originalValue struct {
		value string
		set   bool
	}
	originals := make(map[string]originalValue, len(paths))
	restore := func() {
		for key, original := range originals {
			if original.set {
				_ = os.Setenv(key, original.value)
			} else {
				_ = os.Unsetenv(key)
			}
		}
	}
	for key, value := range paths {
		original, set := os.LookupEnv(key)
		originals[key] = originalValue{value: original, set: set}
		if err := os.Setenv(key, value); err != nil {
			restore()
			_ = os.RemoveAll(home)
			return nil, fmt.Errorf("isolate %s: %w", key, err)
		}
	}

	return func() {
		restore()
		_ = os.RemoveAll(home)
	}, nil
}

func isolatedUserEnvironment(home string) map[string]string {
	dataHome := filepath.Join(home, ".local", "share")
	return map[string]string{
		"HOME":            home,
		"USERPROFILE":     home,
		"APPDATA":         filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join(home, "AppData", "Local"),
		"XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		"XDG_DATA_HOME":   dataHome,
		"XDG_CACHE_HOME":  filepath.Join(home, ".cache"),
		"XDG_STATE_HOME":  filepath.Join(home, ".local", "state"),
	}
}

func isolatedCredentialPaths(home string) map[string]string {
	return map[string]string{
		"CODEX_HOME":            filepath.Join(home, ".codex"),
		"OPENCODE_HOME":         filepath.Join(home, ".local", "share", "opencode"),
		"KIMI_CODE_HOME":        filepath.Join(home, ".kimi-code"),
		"KIMI_CODE_CREDENTIALS": filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json"),
		"KIMI_CREDENTIALS":      filepath.Join(home, ".kimi-code", "credentials", "kimi-code.json"),
		"KIMI_HOME":             filepath.Join(home, ".kimi"),
		"KIMI_SHARE_DIR":        filepath.Join(home, ".kimi"),
		"GROK_HOME":             filepath.Join(home, ".grok"),
	}
}

func isolatedCredentialEnvironment() map[string]string {
	return map[string]string{
		"ANTHROPIC_AUTH_ROOT":     "",
		"ANTHROPIC_API_KEY":       "",
		"ANTHROPIC_TOKEN":         "",
		"ANTIGRAVITY_AUTH_ROOT":   "",
		"ANTIGRAVITY_CSRF_TOKEN":  "",
		"CODEX_AUTH_ROOT":         "",
		"CODEX_TOKEN":             "",
		"COPILOT_TOKEN":           "",
		"CURSOR_TOKEN":            "",
		"DEEPSEEK_API_KEY":        "",
		"GEMINI_ACCESS_TOKEN":     "",
		"GEMINI_REFRESH_TOKEN":    "",
		"GH_TOKEN":                "",
		"GITHUB_TOKEN":            "",
		"GROK_TOKEN":              "",
		"KIMI_CODE_TOKEN":         "",
		"KIMI_TOKEN":              "",
		"MINIMAX_API_KEY":         "",
		"MOONSHOT_API_KEY":        "",
		"OPENAI_API_KEY":          "",
		"OPENCODE_GO_AUTH_COOKIE": "",
		"OPENROUTER_API_KEY":      "",
		"SYNTHETIC_API_KEY":       "",
		"ZAI_API_KEY":             "",
	}
}
