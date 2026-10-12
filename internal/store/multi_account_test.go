package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestProviderAccountAliasDoesNotRenameCredentialIdentity(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	account, err := s.CreateOrRestoreProviderAccount("antigravity", "work")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProviderAccountAlias("antigravity", account.ID, "Acme work"); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveProviderAccount("antigravity", account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Name != "work" || ProviderAccountAlias(*resolved) != "Acme work" {
		t.Fatalf("got name=%q alias=%q", resolved.Name, ProviderAccountAlias(*resolved))
	}
}

// TASK-1: the no-argument call must mean "the provider's default account",
// never "whatever account polled most recently".
func TestQueryLatestDefaultsToTheProviderDefaultAccount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agDefault, err := s.ResolveDefaultProviderAccount("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	agOther, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, snapshot := range []*api.AntigravitySnapshot{
		{AccountID: agDefault.ID, CapturedAt: now, Email: "default@example.com"},
		{AccountID: agOther.ID, CapturedAt: now.Add(time.Minute), Email: "other@example.com"},
	} {
		if _, err := s.InsertAntigravitySnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	agLatest, err := s.QueryLatestAntigravity()
	if err != nil {
		t.Fatal(err)
	}
	if agLatest == nil || agLatest.AccountID != agDefault.ID {
		t.Fatalf("expected default antigravity snapshot, got %#v", agLatest)
	}
}

// TASK-2: LIMIT must be spent inside one account, not shared across accounts.
func TestAccountScopedRangeAndHistorySpendLimitPerAccount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agWork, err := s.CreateOrRestoreProviderAccount("antigravity", "work")
	if err != nil {
		t.Fatal(err)
	}
	agPersonal, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(-400 * time.Minute)
	for i := 0; i < 300; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		// Interleave accounts so a shared LIMIT would truncate each of them.
		for _, pair := range []struct{ antigravity int64 }{{agWork.ID}, {agPersonal.ID}} {
			if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
				AccountID:  pair.antigravity,
				CapturedAt: at,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.CreateAntigravityCycle("gemini", at, nil, agWork.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseAntigravityCycle("gemini", at.Add(time.Second), 1, 1, agWork.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateAntigravityCycle("gemini", at, nil, agPersonal.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseAntigravityCycle("gemini", at.Add(time.Second), 1, 1, agPersonal.ID); err != nil {
			t.Fatal(err)
		}
	}
	start, end := base.Add(-time.Hour), time.Now().UTC().Add(time.Hour)

	agSnaps, err := s.QueryAntigravityRangeForAccount(agWork.ID, start, end, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(agSnaps) != 200 {
		t.Fatalf("antigravity range: got %d snapshots, want 200", len(agSnaps))
	}
	for _, snap := range agSnaps {
		if snap.AccountID != agWork.ID {
			t.Fatalf("antigravity range leaked account %d", snap.AccountID)
		}
	}

	agCycles, err := s.QueryAntigravityCycleHistoryForAccount(agWork.ID, "gemini", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(agCycles) != 200 {
		t.Fatalf("antigravity cycles: got %d, want 200", len(agCycles))
	}
	for _, cycle := range agCycles {
		if cycle.AccountID != agWork.ID {
			t.Fatalf("antigravity cycle leaked account %d", cycle.AccountID)
		}
	}
}

// defaultAccountIDForTest returns the provider's default account so tests that
// insert snapshot rows directly land in the account the default view reads.
func defaultAccountIDForTest(t *testing.T, s *Store, provider string) int64 {
	t.Helper()
	account, err := s.ResolveDefaultProviderAccount(provider)
	if err != nil {
		t.Fatalf("resolve default %s account: %v", provider, err)
	}
	return account.ID
}
