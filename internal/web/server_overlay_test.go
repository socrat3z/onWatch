package web

import (
	"net/http"
	"runtime"
	"testing"
)

func TestForkDefaultServerHost(t *testing.T) {
	t.Parallel()

	if got := forkDefaultServerHost("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("explicit host changed to %q", got)
	}
	want := "0.0.0.0"
	if runtime.GOOS == "windows" {
		want = "127.0.0.1"
	}
	if got := forkDefaultServerHost(""); got != want {
		t.Fatalf("default host = %q, want %q", got, want)
	}
}

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
