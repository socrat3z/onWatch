package web

import (
	"net/http"
	"testing"
)

func TestRegisterForkOverlayRoutesHonorsBasePath(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	registerForkOverlayRoutes(mux, func(path string) string { return "/onwatch" + path }, &Handler{})
	request, err := http.NewRequest(http.MethodGet, "http://example.test/onwatch/api/accounts", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, pattern := mux.Handler(request)
	if pattern != "/onwatch/api/accounts" {
		t.Fatalf("registered pattern = %q", pattern)
	}
}
