package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetTestUserHomeRedirectsPlatformPaths(t *testing.T) {
	home := t.TempDir()
	SetTestUserHome(t, home)

	for _, key := range []string{"HOME", "USERPROFILE"} {
		if got := os.Getenv(key); got != home {
			t.Fatalf("%s = %q, want %q", key, got, home)
		}
	}
	if got := os.Getenv("XDG_DATA_HOME"); got != filepath.Join(home, ".local", "share") {
		t.Fatalf("XDG_DATA_HOME = %q", got)
	}
}

func TestIsolateProcessUserEnvironmentRestoresOriginalValues(t *testing.T) {
	t.Setenv("HOME", "onwatch-original-home")
	t.Setenv("USERPROFILE", "onwatch-original-profile")

	cleanup, err := IsolateProcessUserEnvironment()
	if err != nil {
		t.Fatalf("IsolateProcessUserEnvironment: %v", err)
	}
	cleaned := false
	defer func() {
		if !cleaned {
			cleanup()
		}
	}()

	isolatedHome := os.Getenv("HOME")
	if isolatedHome == "" || isolatedHome == "onwatch-original-home" {
		t.Fatalf("HOME was not isolated: %q", isolatedHome)
	}
	if os.Getenv("USERPROFILE") != isolatedHome {
		t.Fatalf("USERPROFILE did not follow isolated HOME")
	}
	if got := os.Getenv("CODEX_HOME"); got != filepath.Join(isolatedHome, ".codex") {
		t.Fatalf("CODEX_HOME was not isolated: %q", got)
	}

	cleanup()
	cleaned = true
	if got := os.Getenv("HOME"); got != "onwatch-original-home" {
		t.Fatalf("HOME after cleanup = %q", got)
	}
	if got := os.Getenv("USERPROFILE"); got != "onwatch-original-profile" {
		t.Fatalf("USERPROFILE after cleanup = %q", got)
	}
}
