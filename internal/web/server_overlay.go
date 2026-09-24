package web

import (
	"net/http"
	"runtime"
)

// forkDefaultServerHost preserves upstream's wildcard default except on
// Windows, where loopback avoids an unnecessary firewall prompt.
func forkDefaultServerHost(host string) string {
	if host != "" {
		return host
	}
	if runtime.GOOS == "windows" {
		return "127.0.0.1"
	}
	return "0.0.0.0"
}

// registerForkOverlayRoutes is the single route-registration seam for
// downstream HTTP APIs. Fork endpoints belong here instead of in NewServer.
func registerForkOverlayRoutes(mux *http.ServeMux, path func(string) string, handler *Handler) {
	mux.HandleFunc(path("/api/accounts"), handler.ProviderAccounts)
}
