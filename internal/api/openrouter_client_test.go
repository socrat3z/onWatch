package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenRouterFetchUsageIncludesAccountCredits(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/key", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"label":"primary","usage":25.5,"limit":100,"limit_remaining":74.5}}`))
	})
	mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_credits":100.5,"total_usage":25.75}}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := NewOpenRouterClient("test-key", slog.Default(), WithOpenRouterBaseURL(server.URL))
	resp, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.AccountCredits == nil {
		t.Fatal("account credits were not populated")
	}
	if got := resp.AccountCredits.Balance(); got != 74.75 {
		t.Fatalf("balance = %v, want 74.75", got)
	}
}

func TestOpenRouterFetchUsageKeepsKeyUsageWhenCreditsForbidden(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/key", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"usage":12.5,"limit_remaining":7.5}}`))
	})
	mux.HandleFunc("/api/v1/credits", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "management key required", http.StatusForbidden)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := NewOpenRouterClient("test-key", slog.Default(), WithOpenRouterBaseURL(server.URL))
	resp, err := client.FetchUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if resp.Data.Usage != 12.5 {
		t.Fatalf("usage = %v, want 12.5", resp.Data.Usage)
	}
	if resp.AccountCredits != nil {
		t.Fatalf("account credits = %#v, want nil", resp.AccountCredits)
	}
}
