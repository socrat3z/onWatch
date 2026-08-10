package api

import (
	"strings"
	"testing"
)

// TASK-3 AC#1: resident agy processes are capped by agyMaxResidentSessions, so
// adding accounts adds polls but never adds concurrently live CLI processes.
func TestAgyResidentSessionsAreCappedIndependentOfAccountCount(t *testing.T) {
	restore := agyResidentSnapshotForTest()
	defer restore()

	runners := make([]*AntigravityCLIRunner, 0, 5)
	for i := 0; i < 5; i++ {
		runner := NewAntigravityCLIRunner(nil)
		// A non-nil session stands in for a live agy process; admission must
		// evict other holders rather than let them accumulate.
		runner.sess = &agySession{}
		runners = append(runners, runner)
		agyAdmitResident(runner)

		if got := agyResidentCountForTest(); got > agyMaxResidentSessions {
			t.Fatalf("after %d accounts: %d residents registered, cap is %d", i+1, got, agyMaxResidentSessions)
		}
		live := 0
		for _, other := range runners {
			other.mu.Lock()
			if other.sess != nil {
				live++
			}
			other.mu.Unlock()
		}
		if live > agyMaxResidentSessions {
			t.Fatalf("after %d accounts: %d live agy sessions, cap is %d", i+1, live, agyMaxResidentSessions)
		}
	}

	// The most recent admission is the survivor.
	last := runners[len(runners)-1]
	last.mu.Lock()
	defer last.mu.Unlock()
	if last.sess == nil {
		t.Fatal("the most recently admitted runner lost its session")
	}
}

// TASK-3: tearing a session down releases the resident slot for the next poll.
func TestAgyTeardownReleasesTheResidentSlot(t *testing.T) {
	restore := agyResidentSnapshotForTest()
	defer restore()

	runner := NewAntigravityCLIRunner(nil)
	runner.sess = &agySession{}
	agyAdmitResident(runner)
	if agyResidentCountForTest() != 1 {
		t.Fatalf("resident count = %d, want 1", agyResidentCountForTest())
	}

	runner.mu.Lock()
	runner.teardownLocked()
	runner.mu.Unlock()

	if got := agyResidentCountForTest(); got != 0 {
		t.Fatalf("resident count after teardown = %d, want 0", got)
	}
}

// agyResidentSnapshotForTest isolates a test from the process-wide registry.
func agyResidentSnapshotForTest() func() {
	agyResidencyMu.Lock()
	saved := agyResident
	agyResident = nil
	agyResidencyMu.Unlock()
	return func() {
		agyResidencyMu.Lock()
		agyResident = saved
		agyResidencyMu.Unlock()
	}
}

func agyResidentCountForTest() int {
	agyResidencyMu.Lock()
	defer agyResidencyMu.Unlock()
	return len(agyResident)
}

// TASK-5 AC#1/#2: the agy child process environment must never leak the
// daemon's provider secrets. buildAgyEnv is built from an explicit allowlist,
// not os.Environ(), so this asserts the exact key set it can ever produce.
func TestBuildAgyEnvExcludesSecretsAndMatchesAllowlist(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-not-leak")
	t.Setenv("ONWATCH_METRICS_TOKEN", "should-not-leak-either")
	t.Setenv("SYNTHETIC_API_KEY", "also-should-not-leak")
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("TERM", "xterm")

	env := buildAgyEnv(map[string]string{
		"HOME":            "/home/work",
		"XDG_RUNTIME_DIR": "/tmp/onwatch-runtime/antigravity/work",
	})

	seen := make(map[string]string, len(env))
	for _, kv := range env {
		i := 0
		for ; i < len(kv) && kv[i] != '='; i++ {
		}
		key, value := kv[:i], kv[i+1:]
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate key %q in agy env: %v", key, env)
		}
		seen[key] = value

		if strings.Contains(strings.ToUpper(key), "API_KEY") ||
			strings.Contains(strings.ToUpper(key), "_TOKEN") ||
			strings.HasPrefix(key, "ONWATCH_") {
			t.Fatalf("agy env leaked a secret-shaped key %q", key)
		}
	}

	allowed := map[string]bool{
		"PATH": true, "TERM": true, "LANG": true, "LC_ALL": true,
		"AGY_CLI_DISABLE_AUTO_UPDATE": true, "HOME": true, "XDG_RUNTIME_DIR": true,
	}
	for key := range seen {
		if !allowed[key] {
			t.Fatalf("agy env contains %q, not on the allowlist", key)
		}
	}

	if seen["HOME"] != "/home/work" {
		t.Fatalf("HOME override lost: got %q", seen["HOME"])
	}
	if seen["XDG_RUNTIME_DIR"] != "/tmp/onwatch-runtime/antigravity/work" {
		t.Fatalf("XDG_RUNTIME_DIR override lost: got %q", seen["XDG_RUNTIME_DIR"])
	}
	if seen["PATH"] != "/usr/bin" {
		t.Fatalf("PATH not inherited from ambient env: got %q", seen["PATH"])
	}
}

// An account-specific override always wins over the ambient value.
func TestBuildAgyEnvOverrideWinsOverAmbient(t *testing.T) {
	t.Setenv("HOME", "/home/daemon")
	env := buildAgyEnv(map[string]string{"HOME": "/home/work"})
	for _, kv := range env {
		if kv == "HOME=/home/daemon" {
			t.Fatal("ambient HOME leaked instead of being overridden")
		}
	}
	found := false
	for _, kv := range env {
		if kv == "HOME=/home/work" {
			found = true
		}
	}
	if !found {
		t.Fatal("HOME override missing from agy env")
	}
}
