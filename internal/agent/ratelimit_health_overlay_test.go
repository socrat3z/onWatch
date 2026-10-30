package agent

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type poller interface {
	poll(context.Context)
}

func TestGenericRateLimitBackoffPublishesHealthForHTTPProviders(t *testing.T) {
	for _, tc := range []struct {
		provider string
		success  string
		newAgent func(*store.Store, string, *slog.Logger) poller
	}{
		{"deepseek", `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"10.00","granted_balance":"0.00","topped_up_balance":"10.00"}]}`, func(s *store.Store, url string, logger *slog.Logger) poller {
			return NewDeepSeekAgent(api.NewDeepSeekClient("synthetic", logger, api.WithDeepSeekBaseURL(url)), s, nil, time.Minute, logger, nil)
		}},
		{"kimi", `{"user":{"userId":"synthetic"}}`, func(s *store.Store, url string, logger *slog.Logger) poller {
			return NewKimiAgent(api.NewKimiClient("synthetic", logger, api.WithKimiBaseURL(url), api.WithKimiStaticToken("synthetic")), s, nil, time.Minute, logger, nil)
		}},
		{"moonshot", `{"code":0,"data":{"available_balance":10}}`, func(s *store.Store, url string, logger *slog.Logger) poller {
			return NewMoonshotAgent(api.NewMoonshotClient("synthetic", logger, api.WithMoonshotBaseURL(url)), s, nil, time.Minute, logger, nil)
		}},
		{"openrouter", `{"data":{"usage":1,"is_free_tier":false}}`, func(s *store.Store, url string, logger *slog.Logger) poller {
			return NewOpenRouterAgent(api.NewOpenRouterClient("synthetic", logger, api.WithOpenRouterBaseURL(url)), s, nil, time.Minute, logger, nil)
		}},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			s, err := store.New(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var requests atomic.Int32
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/credits") {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":{"total_credits":10,"total_usage":1}}`))
					return
				}
				requests.Add(1)
				if requests.Load() <= 2 {
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.success))
			}))
			server.Listener = listener
			server.Start()
			defer server.Close()
			registry := NewPollStatusRegistry()
			previous := GetPollStatusRegistry()
			SetPollStatusRegistry(registry)
			defer SetPollStatusRegistry(previous)
			agent := tc.newAgent(s, server.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))

			agent.poll(context.Background())
			if outcome, ok := registry.Get(tc.provider, ""); !ok || outcome != PollOutcomeRateLimit {
				t.Fatalf("first 429 outcome = %q, %v; want rate_limit", outcome, ok)
			}
			agent.poll(context.Background())
			if got := requests.Load(); got != 1 {
				t.Fatalf("backoff poll made a request: requests = %d", got)
			}
			if outcome, ok := registry.Get(tc.provider, ""); !ok || outcome != PollOutcomeRateLimit {
				t.Fatalf("skipped poll lost outcome = %q, %v", outcome, ok)
			}
			agent.poll(context.Background())
			if got := requests.Load(); got != 2 {
				t.Fatalf("post-backoff requests = %d, want 2", got)
			}
			agent.poll(context.Background())
			agent.poll(context.Background())
			agent.poll(context.Background())
			if got := requests.Load(); got != 3 {
				t.Fatalf("successful poll requests = %d, want 3; outcome = %+v", got, registry.entriesSnapshot())
			}
			if outcome, ok := registry.Get(tc.provider, ""); ok {
				t.Fatalf("successful poll retained outcome %q", outcome)
			}
		})
	}
}
