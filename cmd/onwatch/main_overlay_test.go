package main

import (
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/agent"
	"github.com/onllm-dev/onwatch/v2/internal/config"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

func TestForkAccountManagersRegisterThroughOneCoordinator(t *testing.T) {
	t.Parallel()

	db, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	managers := setupForkAccountManagers(&config.Config{
		AnthropicAuthRoot:   t.TempDir(),
		AntigravityAuthRoot: t.TempDir(),
		PollInterval:        time.Minute,
	}, db, slog.Default())
	registry := agent.NewAgentManager(slog.Default())
	managers.Register(registry)

	want := []string{"antigravity", "anthropic"}
	if got := registry.RegisteredProviders(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered providers = %v, want %v", got, want)
	}
}
