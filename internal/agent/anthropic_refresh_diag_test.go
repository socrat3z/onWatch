package agent

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

// TestAnthropicAgent_ProactiveRefreshSkipIsVisible verifies a skipped proactive
// refresh is reported at Warn with its reason. These were Debug, which is how a
// token could expire leaving nothing in the log but the 401 storm afterwards.
func TestAnthropicAgent_ProactiveRefreshSkipIsVisible(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	ag := NewAnthropicAgent(api.NewAnthropicClient("tok", logger), str, nil, time.Minute, logger, nil)
	ag.isClaudeCodeRunning = func() bool { return true }
	ag.SetCredentialsRefresh(func() *api.AnthropicCredentials { return nil })

	creds := &api.AnthropicCredentials{
		AccessToken:  "tok",
		RefreshToken: "refresh",
		ExpiresIn:    30 * time.Minute,
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	}
	ag.proactiveRefresh(context.Background(), creds)

	out := logs.String()
	if !strings.Contains(out, "OAuth refresh skipped") {
		t.Errorf("skip was not reported above Debug:\n%s", out)
	}
	if !strings.Contains(out, "Claude Code is running") {
		t.Errorf("skip reason missing:\n%s", out)
	}

	// A repeat of the same reason must not restate itself every poll.
	logs.Reset()
	ag.proactiveRefresh(context.Background(), creds)
	if strings.Contains(logs.String(), "OAuth refresh skipped") {
		t.Errorf("repeated skip was logged again at Warn:\n%s", logs.String())
	}
}

// TestAnthropicAgent_RefreshReadinessWarnsWhenUnavailable verifies the startup
// line says so when this agent cannot refresh at all - the case that previously
// looked identical to a healthy agent until the token expired hours later.
func TestAnthropicAgent_RefreshReadinessWarnsWhenUnavailable(t *testing.T) {
	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer str.Close()

	tests := []struct {
		name      string
		setup     func(*AnthropicAgent)
		wantReady bool
	}{
		{
			name: "no refresh token in the store",
			setup: func(a *AnthropicAgent) {
				a.SetCredentialsRefresh(func() *api.AnthropicCredentials {
					return &api.AnthropicCredentials{AccessToken: "tok", ExpiresAt: time.Now().Add(time.Hour)}
				})
			},
		},
		{
			name:  "no credentials reader configured",
			setup: func(a *AnthropicAgent) {},
		},
		{
			name: "auto refresh disabled",
			setup: func(a *AnthropicAgent) {
				a.SetCredentialsRefresh(func() *api.AnthropicCredentials {
					return &api.AnthropicCredentials{AccessToken: "tok", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
				})
				if err := str.SetSetting(store.SettingAutoRefreshTokens, "false"); err != nil {
					t.Fatalf("SetSetting: %v", err)
				}
			},
		},
		{
			name: "fully configured",
			setup: func(a *AnthropicAgent) {
				a.SetCredentialsRefresh(func() *api.AnthropicCredentials {
					return &api.AnthropicCredentials{AccessToken: "tok", RefreshToken: "r", ExpiresAt: time.Now().Add(time.Hour)}
				})
				if err := str.SetSetting(store.SettingAutoRefreshTokens, "true"); err != nil {
					t.Fatalf("SetSetting: %v", err)
				}
			},
			wantReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := &bytes.Buffer{}
			logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
			ag := NewAnthropicAgent(api.NewAnthropicClient("tok", logger), str, nil, time.Minute, logger, nil)
			ag.isClaudeCodeRunning = func() bool { return false }
			tt.setup(ag)

			ag.logRefreshReadiness()

			out := logs.String()
			ready := strings.Contains(out, "OAuth refresh is available")
			unavailable := strings.Contains(out, "OAuth refresh is NOT available")
			if ready == unavailable {
				t.Fatalf("readiness was not reported exactly once:\n%s", out)
			}
			if ready != tt.wantReady {
				t.Errorf("reported ready=%v, want %v:\n%s", ready, tt.wantReady, out)
			}
		})
	}
}
