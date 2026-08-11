package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type anthropicManagerFixture struct {
	manager *AnthropicAgentManager
	store   *store.Store
	root    string
}

// newAnthropicManagerFixture builds a manager over a temp credential root. No
// real Claude home, keychain or network is involved: every account's token is
// invented by the test and polling is disabled unless a case opts in.
func newAnthropicManagerFixture(t *testing.T) *anthropicManagerFixture {
	t.Helper()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	root := t.TempDir()
	manager := NewAnthropicAgentManager(str, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	manager.SetAuthRoot(root)
	manager.SetAccountPollingCheck(func(int64) bool { return false })

	ctx, cancel := context.WithCancel(context.Background())
	manager.mu.Lock()
	manager.ctx = ctx
	manager.mu.Unlock()
	t.Cleanup(func() {
		manager.stopAll()
		cancel()
	})

	return &anthropicManagerFixture{manager: manager, store: str, root: root}
}

// writeAnthropicHome creates <root>/<alias>/.claude, with a credentials file
// only when token is non-empty - an alias without one is exactly the "account
// registered but never started" case.
func writeAnthropicHome(t *testing.T, root, alias, token string) {
	t.Helper()
	dir := filepath.Join(root, alias, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if token == "" {
		return
	}
	body := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":%q,"refreshToken":"refresh-%s","expiresAt":%d}}`,
		token, alias, time.Now().Add(time.Hour).UnixMilli())
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write credentials: %v", err)
	}
}

func accountByName(t *testing.T, str *store.Store, provider, name string) store.ProviderAccount {
	t.Helper()
	accounts, err := str.QueryProviderAccounts(provider)
	if err != nil {
		t.Fatalf("QueryProviderAccounts: %v", err)
	}
	for _, acc := range accounts {
		if acc.Name == name {
			return acc
		}
	}
	t.Fatalf("no %s account named %q in %+v", provider, name, accounts)
	return store.ProviderAccount{}
}

func TestAnthropicManagerSoftDeletesAccountThatNeverStarted(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	writeAnthropicHome(t, fx.root, "work", "")
	fx.manager.Reload()

	if acc := accountByName(t, fx.store, "anthropic", "work"); acc.DeletedAt != nil {
		t.Fatalf("account with missing credentials should stay active while its directory exists")
	}
	fx.manager.mu.Lock()
	running := len(fx.manager.running)
	fx.manager.mu.Unlock()
	if running != 0 {
		t.Fatalf("credential-less account must not start an agent, got %d running", running)
	}

	if err := os.RemoveAll(filepath.Join(fx.root, "work")); err != nil {
		t.Fatalf("remove home: %v", err)
	}
	fx.manager.Reload()

	if acc := accountByName(t, fx.store, "anthropic", "work"); acc.DeletedAt == nil {
		t.Fatal("removing the directory of a never-started account must soft-delete it")
	}
}

func TestAnthropicManagerSoftDeletesRunningAccountOnRemoval(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	writeAnthropicHome(t, fx.root, "work", "token-work")
	fx.manager.Reload()

	fx.manager.mu.Lock()
	_, started := fx.manager.running["work"]
	fx.manager.mu.Unlock()
	if !started {
		t.Fatal("an account with readable credentials must start an agent")
	}

	if err := os.RemoveAll(filepath.Join(fx.root, "work")); err != nil {
		t.Fatalf("remove home: %v", err)
	}
	fx.manager.Reload()

	fx.manager.mu.Lock()
	_, stillRunning := fx.manager.running["work"]
	fx.manager.mu.Unlock()
	if stillRunning {
		t.Fatal("a removed account must be cancelled")
	}
	if acc := accountByName(t, fx.store, "anthropic", "work"); acc.DeletedAt == nil {
		t.Fatal("a removed running account must be soft-deleted")
	}
}

func TestAnthropicManagerRestoresAccountWhenDirectoryReappears(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	writeAnthropicHome(t, fx.root, "work", "")
	fx.manager.Reload()
	first := accountByName(t, fx.store, "anthropic", "work")

	if err := os.RemoveAll(filepath.Join(fx.root, "work")); err != nil {
		t.Fatalf("remove home: %v", err)
	}
	fx.manager.Reload()
	if acc := accountByName(t, fx.store, "anthropic", "work"); acc.DeletedAt == nil {
		t.Fatal("expected soft delete before restore")
	}

	writeAnthropicHome(t, fx.root, "work", "")
	fx.manager.Reload()

	restored := accountByName(t, fx.store, "anthropic", "work")
	if restored.DeletedAt != nil {
		t.Fatal("a reappearing directory must restore its account")
	}
	if restored.ID != first.ID {
		t.Fatalf("restore must reuse account %d, got %d - history would be stranded", first.ID, restored.ID)
	}
}

func TestAnthropicManagerKeepsLegacyDefaultAccount(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	legacy, err := fx.store.EnsureDefaultProviderAccount("anthropic")
	if err != nil {
		t.Fatalf("EnsureDefaultProviderAccount: %v", err)
	}
	writeAnthropicHome(t, fx.root, "work", "token-work")
	fx.manager.Reload()

	acc, err := fx.store.GetProviderAccountByID(legacy.ID)
	if err != nil {
		t.Fatalf("GetProviderAccountByID: %v", err)
	}
	if acc.DeletedAt != nil {
		t.Fatal("the legacy default account is not directory-backed and must survive reconciliation")
	}
}

func TestAnthropicManagerIgnoresUnavailableRoot(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	writeAnthropicHome(t, fx.root, "work", "")
	fx.manager.Reload()

	fx.manager.SetAuthRoot(filepath.Join(fx.root, "does-not-exist"))
	fx.manager.Reload()

	if acc := accountByName(t, fx.store, "anthropic", "work"); acc.DeletedAt != nil {
		t.Fatal("an unmounted root must not retire every account")
	}
}

func TestAnthropicManagerPausingOneAccountLeavesTheOtherPolling(t *testing.T) {
	fx := newAnthropicManagerFixture(t)

	var mu sync.Mutex
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.Header.Get("Authorization")] = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)

	fx.manager.mu.Lock()
	fx.manager.clientOpts = []api.AnthropicOption{api.WithAnthropicBaseURL(server.URL)}
	fx.manager.mu.Unlock()

	writeAnthropicHome(t, fx.root, "paused", "token-paused")
	writeAnthropicHome(t, fx.root, "live", "token-live")

	// Register both accounts first so the pause predicate can name one by ID.
	fx.manager.SetAccountPollingCheck(func(int64) bool { return false })
	fx.manager.Reload()
	paused := accountByName(t, fx.store, "anthropic", "paused")
	fx.manager.stopAll()

	fx.manager.SetAccountPollingCheck(func(id int64) bool { return id != paused.ID })
	fx.manager.Reload()

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		live := seen["Bearer token-live"]
		stale := seen["Bearer token-paused"]
		mu.Unlock()
		if stale {
			t.Fatal("a paused account must not poll")
		}
		if live {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the unpaused account never polled - one paused account stopped another")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAnthropicManagerSettersAreSafeDuringReload(t *testing.T) {
	fx := newAnthropicManagerFixture(t)
	writeAnthropicHome(t, fx.root, "work", "token-work")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				fx.manager.Reload()
			}
		}()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			fx.manager.SetAuthRoot(fx.root)
		}
	}()
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			fx.manager.SetAccountPollingCheck(func(int64) bool { return false })
			fx.manager.SetNotifier(nil)
		}
	}()
	wg.Wait()
}
