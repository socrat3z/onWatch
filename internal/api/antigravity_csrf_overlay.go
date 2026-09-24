package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Fork overlay: local-RPC CSRF support for the managed agy session.
//
// From agy 1.2.2 onward, the CLI's embedded language server rejects every
// Connect-RPC call that lacks a matching X-Codeium-Csrf-Token header with
// 401 {"code":"unauthenticated","message":"missing CSRF token"}. The token is
// minted in-process and never exposed, and seeding ANTIGRAVITY_CSRF_TOKEN in
// the environment does not change it. The hidden --csrf_token launch flag does:
// agy adopts the caller's value, so the runner mints a random token per launch,
// passes it on the command line, and echoes it on every RPC.
//
// agy before 1.2 aborts on an undefined flag, so the flag is gated on the
// binary's reported version. The unrelated Google OAuth session is untouched.

// ErrAgyCSRFRejected reports that agy refused the local RPC token. It is a
// launch/compatibility failure, not a sign-in failure: re-authenticating the
// account does not help.
var ErrAgyCSRFRejected = errors.New("antigravity cli: agy rejected the local RPC CSRF token (unsupported agy version?)")

const (
	agyCSRFFlag            = "--csrf_token"
	agyCSRFHeader          = "X-Codeium-Csrf-Token"
	agyVersionProbeTimeout = 30 * time.Second
)

// agyCSRFMinVersion is the first agy line that both accepts --csrf_token and
// needs it. 1.2.0 and 1.2.1 accept the flag without enforcing the token.
var agyCSRFMinVersion = [3]int{1, 2, 0}

var agyVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// agyVersionCache maps a binary identity (path, size, mtime) to its
// `agy --version` output, so the ~130 MB version probe runs once per install
// rather than once per relaunch.
var agyVersionCache sync.Map

// agyVersionAcceptsCSRFFlag reports whether the version output belongs to an
// agy that accepts --csrf_token. known is false when no version could be
// parsed; accepts is then true, because every agy still being shipped needs
// the flag and would otherwise fail with a 401 on every poll.
func agyVersionAcceptsCSRFFlag(out string) (accepts, known bool) {
	m := agyVersionPattern.FindStringSubmatch(out)
	if m == nil {
		return true, false
	}
	var v [3]int
	for i := 0; i < 3; i++ {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	for i := 0; i < 3; i++ {
		if v[i] != agyCSRFMinVersion[i] {
			return v[i] > agyCSRFMinVersion[i], true
		}
	}
	return true, true
}

// agyVersion returns the cached `agy --version` output for binPath. The probe
// runs with the runner's allowlisted environment, never os.Environ().
func (r *AntigravityCLIRunner) agyVersion(binPath string) (string, error) {
	key := binPath
	if fi, err := os.Stat(binPath); err == nil {
		key = binPath + "|" + strconv.FormatInt(fi.Size(), 10) + "|" + strconv.FormatInt(fi.ModTime().UnixNano(), 10)
	}
	if v, ok := agyVersionCache.Load(key); ok {
		return v.(string), nil
	}
	ctx, cancel := context.WithTimeout(r.rootCtx, agyVersionProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, "--version")
	cmd.Env = r.env
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	version := string(bytes.TrimSpace(out))
	agyVersionCache.Store(key, version)
	return version, nil
}

// agyCSRFArgs mints the token for the session about to launch, stores it on
// the runner, and returns the launch flags carrying it. It returns no flags,
// and clears the token, for an agy too old to accept --csrf_token. The token
// is a local secret and is never logged. Caller must hold r.mu.
func (r *AntigravityCLIRunner) agyCSRFArgs(binPath string) []string {
	r.csrfToken = ""
	version, err := r.agyVersion(binPath)
	if err != nil {
		r.logger.Warn("agy --version failed; assuming a CSRF-enforcing agy", "error", err)
	}
	accepts, known := agyVersionAcceptsCSRFFlag(version)
	if err == nil && !known {
		r.logger.Warn("could not parse agy version; assuming a CSRF-enforcing agy", "output", version)
	}
	if !accepts {
		r.logger.Debug("agy predates local RPC CSRF; launching without a token", "version", version)
		return nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		r.logger.Error("could not generate agy CSRF token; agy will reject local RPCs", "error", err)
		return nil
	}
	r.csrfToken = hex.EncodeToString(buf)
	return []string{agyCSRFFlag, r.csrfToken}
}

// setAgyCSRFHeader attaches the session token. An empty token sends no header,
// which is what an agy older than 1.2 expects.
func setAgyCSRFHeader(req *http.Request, token string) {
	if token != "" {
		req.Header.Set(agyCSRFHeader, token)
	}
}

// isAgyCSRFRejection distinguishes agy's CSRF interceptor ("missing CSRF
// token" / "invalid CSRF token") from a genuine sign-in failure, so the runner
// can stop waiting for readiness instead of retrying for the full timeout.
func isAgyCSRFRejection(status int, body []byte) bool {
	return status == http.StatusUnauthorized && bytes.Contains(bytes.ToLower(body), []byte("csrf token"))
}
