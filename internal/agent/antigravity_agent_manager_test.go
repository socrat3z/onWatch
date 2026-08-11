package agent

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/store"
)

type antigravityManagerFixture struct {
	manager *AntigravityAgentManager
	store   *store.Store
	root    string
}

// newAntigravityManagerFixture keeps polling disabled by default, which is what
// stops the agent from ever launching the real agy CLI during tests.
func newAntigravityManagerFixture(t *testing.T) *antigravityManagerFixture {
	t.Helper()

	str, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { str.Close() })

	root := t.TempDir()
	manager := NewAntigravityAgentManager(str, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

	return &antigravityManagerFixture{manager: manager, store: str, root: root}
}

func writeAntigravityHome(t *testing.T, root, alias string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, alias, ".gemini"), 0o700); err != nil {
		t.Fatalf("mkdir antigravity home: %v", err)
	}
}

func TestAntigravityManagerStartsRemovesAndRestoresAccounts(t *testing.T) {
	fx := newAntigravityManagerFixture(t)

	writeAntigravityHome(t, fx.root, "work")
	fx.manager.Reload()

	fx.manager.mu.Lock()
	_, started := fx.manager.running["work"]
	fx.manager.mu.Unlock()
	if !started {
		t.Fatal("a discovered account home must start an agent")
	}
	first := accountByName(t, fx.store, "antigravity", "work")

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
	if acc := accountByName(t, fx.store, "antigravity", "work"); acc.DeletedAt == nil {
		t.Fatal("a removed account must be soft-deleted")
	}

	writeAntigravityHome(t, fx.root, "work")
	fx.manager.Reload()

	restored := accountByName(t, fx.store, "antigravity", "work")
	if restored.DeletedAt != nil || restored.ID != first.ID {
		t.Fatalf("a reappearing home must restore account %d, got %+v", first.ID, restored)
	}
}

func TestAntigravityManagerSoftDeletesAccountRegisteredWithoutAnAgent(t *testing.T) {
	fx := newAntigravityManagerFixture(t)

	// An account that the daemon knows about but never had a home for - e.g.
	// registered by an earlier run, then its directory removed while stopped.
	orphan, err := fx.store.CreateOrRestoreProviderAccount("antigravity", "gone")
	if err != nil {
		t.Fatalf("CreateOrRestoreProviderAccount: %v", err)
	}
	writeAntigravityHome(t, fx.root, "work")
	fx.manager.Reload()

	acc, err := fx.store.GetProviderAccountByID(orphan.ID)
	if err != nil {
		t.Fatalf("GetProviderAccountByID: %v", err)
	}
	if acc.DeletedAt == nil {
		t.Fatal("an account with no directory and no agent must still be soft-deleted")
	}
}

func TestAntigravityManagerKeepsLegacyDefaultAccount(t *testing.T) {
	fx := newAntigravityManagerFixture(t)

	legacy, err := fx.store.EnsureDefaultProviderAccount("antigravity")
	if err != nil {
		t.Fatalf("EnsureDefaultProviderAccount: %v", err)
	}
	writeAntigravityHome(t, fx.root, "work")
	fx.manager.Reload()

	acc, err := fx.store.GetProviderAccountByID(legacy.ID)
	if err != nil {
		t.Fatalf("GetProviderAccountByID: %v", err)
	}
	if acc.DeletedAt != nil {
		t.Fatal("the legacy default account must survive reconciliation")
	}
}

func TestAntigravityManagerIgnoresUnavailableRoot(t *testing.T) {
	fx := newAntigravityManagerFixture(t)

	writeAntigravityHome(t, fx.root, "work")
	fx.manager.Reload()

	fx.manager.SetAuthRoot(filepath.Join(fx.root, "does-not-exist"))
	fx.manager.Reload()

	if acc := accountByName(t, fx.store, "antigravity", "work"); acc.DeletedAt != nil {
		t.Fatal("an unmounted root must not retire every account")
	}
}

func TestAntigravityManagerAsksThePollingCheckPerAccount(t *testing.T) {
	fx := newAntigravityManagerFixture(t)

	var mu sync.Mutex
	var asked []int64
	fx.manager.SetAccountPollingCheck(func(id int64) bool {
		mu.Lock()
		asked = append(asked, id)
		mu.Unlock()
		return false
	})

	writeAntigravityHome(t, fx.root, "work")
	writeAntigravityHome(t, fx.root, "personal")
	fx.manager.Reload()

	work := accountByName(t, fx.store, "antigravity", "work")
	personal := accountByName(t, fx.store, "antigravity", "personal")

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		ids := append([]int64(nil), asked...)
		mu.Unlock()
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if len(ids) >= 2 && contains(ids, work.ID) && contains(ids, personal.ID) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("each account must consult the pause predicate with its own ID, saw %v (want %d and %d)", ids, work.ID, personal.ID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func contains(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestAntigravityManagerSettersAreSafeDuringReload(t *testing.T) {
	fx := newAntigravityManagerFixture(t)
	writeAntigravityHome(t, fx.root, "work")

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

func TestAccountCLIEnvCreatesAPrivateRuntimeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TMPDIR", t.TempDir())

	env := accountCLIEnv(home, "personal", slog.New(slog.NewTextHandler(io.Discard, nil)))

	if env["HOME"] != home {
		t.Fatalf("HOME = %q, want %q", env["HOME"], home)
	}
	runtimeDir := env["XDG_RUNTIME_DIR"]
	if runtimeDir == "" {
		t.Fatal("XDG_RUNTIME_DIR must be set for a named account")
	}
	info, err := os.Stat(runtimeDir)
	if err != nil {
		t.Fatalf("agy would start with a XDG_RUNTIME_DIR that does not exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", runtimeDir)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory mode = %o, want 700 - GNOME Keyring rejects anything looser", info.Mode().Perm())
	}

	// A second account must not share the first account's runtime directory.
	other := accountCLIEnv(home, "work", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if other["XDG_RUNTIME_DIR"] == runtimeDir {
		t.Fatal("accounts must not share a runtime directory")
	}
	// The path is derived from the account name only, so it survives a restart.
	if again := accountCLIEnv(home, "personal", slog.New(slog.NewTextHandler(io.Discard, nil))); again["XDG_RUNTIME_DIR"] != runtimeDir {
		t.Fatalf("runtime directory must be stable across restarts: %q then %q", runtimeDir, again["XDG_RUNTIME_DIR"])
	}
}

func TestAccountCLIEnvStaysEmptyWithoutAnAccountHome(t *testing.T) {
	if env := accountCLIEnv("", "personal", slog.New(slog.NewTextHandler(io.Discard, nil))); len(env) != 0 {
		t.Fatalf("the ambient agent must inherit the daemon environment, got %v", env)
	}
}
