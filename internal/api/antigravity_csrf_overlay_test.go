package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// writeFakeAgy writes a stub agy that prints version and records each call in
// calls.txt beside it, so tests can assert how often the version is probed.
func writeFakeAgy(t *testing.T, version string) (binPath, callsPath string) {
	t.Helper()
	dir := t.TempDir()
	callsPath = filepath.Join(dir, "calls.txt")
	if runtime.GOOS == "windows" {
		binPath = filepath.Join(dir, "agy.cmd")
		script := "@echo x>>\"%~dp0calls.txt\"\r\n@echo " + version + "\r\n"
		if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake agy: %v", err)
		}
		return binPath, callsPath
	}
	binPath = filepath.Join(dir, "agy")
	script := "#!/bin/sh\necho x >> \"$(dirname \"$0\")/calls.txt\"\necho '" + version + "'\n"
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake agy: %v", err)
	}
	return binPath, callsPath
}

func countCalls(t *testing.T, callsPath string) int {
	t.Helper()
	data, err := os.ReadFile(callsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read calls: %v", err)
	}
	return strings.Count(string(data), "x")
}

func newTestAgyRunner(t *testing.T) *AntigravityCLIRunner {
	t.Helper()
	r := NewAntigravityCLIRunnerWithEnv(discardLoggerCommands(), nil)
	t.Cleanup(r.Stop)
	return r
}

func TestAgyVersionAcceptsCSRFFlag(t *testing.T) {
	tests := []struct {
		out     string
		accepts bool
		known   bool
	}{
		{"1.2.7", true, true},
		{"1.2.0\n", true, true},
		{"1.2.10", true, true},
		{"2.0.0", true, true},
		{"agy version 1.3.1 (linux)", true, true},
		{"1.1.11", false, true},
		{"1.1.28", false, true},
		{"0.9", false, true},
		{"", true, false},
		{"garbage", true, false},
	}
	for _, tt := range tests {
		accepts, known := agyVersionAcceptsCSRFFlag(tt.out)
		if accepts != tt.accepts || known != tt.known {
			t.Errorf("agyVersionAcceptsCSRFFlag(%q) = (%v, %v), want (%v, %v)", tt.out, accepts, known, tt.accepts, tt.known)
		}
	}
}

func TestAgyCSRFArgs_NewAgyGetsFreshTokenPerLaunch(t *testing.T) {
	bin, _ := writeFakeAgy(t, "1.2.7")
	r := newTestAgyRunner(t)

	args := r.agyCSRFArgs(bin)
	if len(args) != 2 || args[0] != "--csrf_token" {
		t.Fatalf("args = %q, want [--csrf_token <token>]", args)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(args[1]) {
		t.Fatalf("token %q is not 32 random bytes in hex", args[1])
	}
	if r.csrfToken != args[1] {
		t.Fatalf("runner token %q does not match launch flag %q", r.csrfToken, args[1])
	}

	first := r.csrfToken
	args2 := r.agyCSRFArgs(bin)
	if len(args2) != 2 || args2[1] == first || r.csrfToken != args2[1] {
		t.Fatalf("relaunch must mint a new token: first=%q second=%q runner=%q", first, args2, r.csrfToken)
	}
}

func TestAgyCSRFArgs_OldAgyGetsNoFlag(t *testing.T) {
	bin, _ := writeFakeAgy(t, "1.1.11")
	r := newTestAgyRunner(t)
	r.csrfToken = "stale-from-previous-session"

	if args := r.agyCSRFArgs(bin); len(args) != 0 {
		t.Fatalf("agy < 1.2 aborts on unknown flags; args = %q, want none", args)
	}
	if r.csrfToken != "" {
		t.Fatalf("runner token = %q, want empty for agy < 1.2", r.csrfToken)
	}
}

func TestAgyCSRFArgs_UnknownVersionStillSendsFlag(t *testing.T) {
	bin, _ := writeFakeAgy(t, "unparseable")
	r := newTestAgyRunner(t)

	if args := r.agyCSRFArgs(bin); len(args) != 2 {
		t.Fatalf("unknown version should assume a current agy; args = %q", args)
	}
}

func TestAgyCSRFArgs_VersionProbedOncePerBinary(t *testing.T) {
	bin, calls := writeFakeAgy(t, "1.2.7")
	r := newTestAgyRunner(t)

	for i := 0; i < 3; i++ {
		r.agyCSRFArgs(bin)
	}
	if n := countCalls(t, calls); n != 1 {
		t.Fatalf("agy --version ran %d times, want 1 (cached per binary)", n)
	}
}

func TestAgyRunnerPost_SendsCSRFHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req.Header.Get("X-Codeium-Csrf-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestAgyRunner(t)
	r.csrfToken = "session-token"
	if _, status, err := r.post(context.Background(), srv.URL, agyQuotaSummaryRPC); err != nil || status != http.StatusOK {
		t.Fatalf("post: status=%d err=%v", status, err)
	}
	if got != "session-token" {
		t.Fatalf("X-Codeium-Csrf-Token = %q, want session-token", got)
	}
}

func TestAgyRunnerPost_NoTokenNoHeader(t *testing.T) {
	present := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, present = req.Header["X-Codeium-Csrf-Token"]
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r := newTestAgyRunner(t)
	if _, _, err := r.post(context.Background(), srv.URL, agyQuotaSummaryRPC); err != nil {
		t.Fatalf("post: %v", err)
	}
	if present {
		t.Fatal("agy < 1.2 must not receive a CSRF header")
	}
}

func TestIsAgyCSRFRejection(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusUnauthorized, `{"code":"unauthenticated","message":"missing CSRF token"}`, true},
		{http.StatusUnauthorized, `{"code":"unauthenticated","message":"invalid CSRF token"}`, true},
		{http.StatusUnauthorized, `{"code":"unauthenticated","message":"login required"}`, false},
		{http.StatusOK, `{"message":"csrf"}`, false},
		{http.StatusForbidden, `missing CSRF token`, false},
	}
	for _, tt := range tests {
		if got := isAgyCSRFRejection(tt.status, []byte(tt.body)); got != tt.want {
			t.Errorf("isAgyCSRFRejection(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}
