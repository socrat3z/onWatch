package web

import (
	"net/http"
)

// registerForkOverlayRoutes is the single route-registration seam for
// downstream HTTP APIs. Fork endpoints belong here instead of in NewServer.
func registerForkOverlayRoutes(mux *http.ServeMux, path func(string) string, handler *Handler) {
	mux.HandleFunc(path("/api/accounts"), handler.ProviderAccounts)
}
