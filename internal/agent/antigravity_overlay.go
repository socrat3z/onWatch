package agent

import (
	"log/slog"
	"os"
	"path/filepath"
)

// accountCLIEnv builds the isolated environment one account's agy process runs
// under. The runtime directory is created here because nothing else does:
// docker-entrypoint-with-user-env.sh creates only the "default" path, and GNOME
// Keyring refuses a XDG_RUNTIME_DIR that is missing or not 0700 - which would
// silently drop every non-default account's keyring state.
func accountCLIEnv(accountHome, accountName string, logger *slog.Logger) map[string]string {
	env := map[string]string{}
	if accountHome == "" {
		return env
	}
	env["HOME"] = accountHome
	runtimeDir := filepath.Join(os.TempDir(), "onwatch-runtime", "antigravity", accountName)
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		logger.Warn("create Antigravity runtime directory", "account", accountName, "path", runtimeDir, "error", err)
		return env
	}
	// MkdirAll leaves an existing directory's mode alone, so re-assert 0700.
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		logger.Warn("tighten Antigravity runtime directory", "account", accountName, "path", runtimeDir, "error", err)
	}
	env["XDG_RUNTIME_DIR"] = runtimeDir
	return env
}
