package agent

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// claudeCodeScanTimeout bounds the process listing used by IsClaudeCodeRunning
// so a wedged `ps` can never stall a poll cycle.
const claudeCodeScanTimeout = 5 * time.Second

// isClaudeCodeCommandLine reports whether a full process command line belongs to
// the Claude Code CLI.
//
// The check must be narrow: a false positive suppresses onWatch's OAuth refresh
// (see proactiveRefresh), which is how issue #111 left polling paused for days.
// A plain substring match on "claude" matches the Claude desktop app's Electron
// helpers, MCP servers under ~/.claude/plugins, and anything referencing a
// .claude path - all of which are present without any Claude Code session.
func isClaudeCodeCommandLine(cmdline string) bool {
	line := strings.TrimSpace(cmdline)
	if line == "" {
		return false
	}

	// Node/Bun-launched CLI: `node .../@anthropic-ai/claude-code/cli.js`.
	lower := strings.ToLower(line)
	if strings.Contains(lower, "@anthropic-ai/claude-code") || strings.Contains(lower, "claude-code/cli.js") {
		return true
	}

	// Electron desktop app: macOS bundles live inside *.app/Contents, and every
	// helper process carries a --type= flag. Neither is ever the CLI.
	if strings.Contains(line, ".app/Contents/") || strings.Contains(line, "--type=") {
		return false
	}

	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}

	// The CLI executable itself. The comparison is case-sensitive on purpose:
	// the CLI is "claude", the macOS desktop binary is "Claude".
	base := filepath.Base(fields[0])
	if runtime.GOOS == "windows" && (strings.EqualFold(base, "claude.exe") || strings.EqualFold(base, "claude")) {
		return true
	}
	if base == "claude" {
		return true
	}

	// A JS runtime running the CLI through its bin entry. npm/bun global
	// installs put a symlink at e.g. /usr/local/bin/claude pointing at
	// cli.js; because the kernel hands the interpreter the *unresolved*
	// path from the shebang, ps reports `node /usr/local/bin/claude` and
	// the resolved "@anthropic-ai/claude-code" path never appears. Missing
	// this would let onWatch refresh while a real session is live, burning
	// the session's one-time-use refresh token.
	if !isJSRuntime(base) {
		return false
	}
	for _, arg := range fields[1:] {
		if strings.HasPrefix(arg, "-") {
			continue // interpreter flag
		}
		if !strings.ContainsAny(arg, "/.") {
			continue // subcommand such as `deno run`
		}
		// First script-like argument decides.
		return filepath.Base(arg) == "claude"
	}
	return false
}

// isJSRuntime reports whether an executable basename is a JavaScript runtime
// that could be hosting the Claude Code CLI.
func isJSRuntime(base string) bool {
	switch strings.ToLower(strings.TrimSuffix(base, ".exe")) {
	case "node", "nodejs", "bun", "deno":
		return true
	}
	return false
}

// matchClaudeCodeProcess returns the command line of the first Claude Code CLI
// process in a listing, or "" when there is none.
//
// The command line is kept so callers can name what matched. A false positive
// here silently disables OAuth refresh, and "Claude Code is running" with no
// evidence attached is not something an operator can act on.
func matchClaudeCodeProcess(psOutput []byte) string {
	for _, line := range bytes.Split(psOutput, []byte("\n")) {
		if candidate := strings.TrimSpace(string(line)); isClaudeCodeCommandLine(candidate) {
			return candidate
		}
	}
	return ""
}

// scanForClaudeCode reports whether any line of a process listing is a Claude
// Code CLI process.
func scanForClaudeCode(psOutput []byte) bool {
	return matchClaudeCodeProcess(psOutput) != ""
}

// ClaudeCodeProcess returns the command line of a running Claude Code CLI
// process, or "" when none is running.
//
// When Claude Code is running, onWatch skips the OAuth token exchange to avoid
// competing for the same refresh token - a refresh by onWatch invalidates
// Claude Code's pending refresh, causing it to get invalid_grant and
// re-authenticate. The command line is returned rather than a bare bool so a
// skipped refresh can name what blocked it: a false positive here silently
// disables refresh, which is indistinguishable from a broken login in the logs.
// Exported as a package-level variable so tests can override it.
//
// On unix the full command line of every process is inspected (see
// isClaudeCodeCommandLine) rather than substring-matching "claude" with pgrep,
// which matched the Claude desktop app and any process referencing a .claude
// path. See https://github.com/onllm-dev/onWatch/issues/111.
//
// Note this stays non-empty on hosts where the Claude Code background daemon
// (`claude daemon run`, `claude bg-spare`, `claude bg-pty-host`) is resident
// even with no interactive session. That is intentional: those processes hold
// the same credentials and refresh them on their own schedule, so onWatch must
// still keep its hands off the refresh token. tryOAuthRecovery keeps that
// block too - the lock is advisory, so a native `claude` takes no lock and both
// sides would spend the same token - but it first tries to adopt whatever that
// process has already written, and lifts the block only where the CLI is known
// to be flock-wrapped (claudeCLISharesCredentialLock).
var ClaudeCodeProcess = func() string {
	ctx, cancel := context.WithTimeout(context.Background(), claudeCodeScanTimeout)
	defer cancel()

	if runtime.GOOS == "windows" {
		// Windows tasklist cannot report command lines without a much heavier
		// WMI/PowerShell query, so this stays a process-name match. It shares
		// the name with the desktop app, which is a known limitation.
		cmd := exec.CommandContext(ctx, "cmd", "/C", `tasklist /FI "IMAGENAME eq claude.exe" /NH 2>nul | findstr /I "claude.exe"`)
		if cmd.Run() == nil {
			return "claude.exe"
		}
		return ""
	}

	// `ps -Ao args=` is POSIX and prints the full command line of every process
	// on both macOS and Linux.
	out, err := exec.CommandContext(ctx, "ps", "-Ao", "args=").Output()
	if err != nil {
		// Treat an unusable process listing as "not running": the OAuth guards
		// downstream (rate limit backoff, invalid_grant) still bound refreshes.
		return ""
	}
	return matchClaudeCodeProcess(out)
}

// IsClaudeCodeRunning is the boolean form of ClaudeCodeProcess. Kept as a
// separate variable because tests override it directly.
var IsClaudeCodeRunning = func() bool { return ClaudeCodeProcess() != "" }
