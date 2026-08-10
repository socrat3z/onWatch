package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestAnthropicSnapshotsStayScopedToTheirProviderAccount(t *testing.T) {
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	work, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, snapshot := range []*api.AnthropicSnapshot{
		{AccountID: work.ID, CapturedAt: now, Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 20}}},
		{AccountID: personal.ID, CapturedAt: now.Add(time.Minute), Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 70}}},
	} {
		if _, err := s.InsertAnthropicSnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.QueryLatestAnthropic(work.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.AccountID != work.ID || latest.Quotas[0].Utilization != 20 {
		t.Fatalf("work snapshot leaked: %#v", latest)
	}
}

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
	defaultAccount, err := s.ResolveDefaultProviderAccount("anthropic")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, snapshot := range []*api.AnthropicSnapshot{
		{AccountID: defaultAccount.ID, CapturedAt: now, Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 20}}},
		{AccountID: other.ID, CapturedAt: now.Add(time.Minute), Quotas: []api.AnthropicQuota{{Name: "five_hour", Utilization: 70}}},
	} {
		if _, err := s.InsertAnthropicSnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.QueryLatestAnthropic()
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.AccountID != defaultAccount.ID {
		t.Fatalf("expected default account snapshot, got %#v", latest)
	}

	agDefault, err := s.ResolveDefaultProviderAccount("antigravity")
	if err != nil {
		t.Fatal(err)
	}
	agOther, err := s.CreateOrRestoreProviderAccount("antigravity", "personal")
	if err != nil {
		t.Fatal(err)
	}
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
	work, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatal(err)
	}
	personal, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
	if err != nil {
		t.Fatal(err)
	}
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
		for _, pair := range []struct {
			anthropic   int64
			antigravity int64
		}{{work.ID, agWork.ID}, {personal.ID, agPersonal.ID}} {
			if _, err := s.InsertAnthropicSnapshot(&api.AnthropicSnapshot{
				AccountID:  pair.anthropic,
				CapturedAt: at,
				Quotas:     []api.AnthropicQuota{{Name: "five_hour", Utilization: float64(i % 100)}},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.InsertAntigravitySnapshot(&api.AntigravitySnapshot{
				AccountID:  pair.antigravity,
				CapturedAt: at,
			}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.CreateAnthropicCycle("five_hour", at, nil, work.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseAnthropicCycle("five_hour", at.Add(time.Second), 1, 1, work.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateAnthropicCycle("five_hour", at, nil, personal.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.CloseAnthropicCycle("five_hour", at.Add(time.Second), 1, 1, personal.ID); err != nil {
			t.Fatal(err)
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

	snaps, err := s.QueryAnthropicRangeForAccount(work.ID, start, end, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 200 {
		t.Fatalf("anthropic range: got %d snapshots, want 200", len(snaps))
	}
	for _, snap := range snaps {
		if snap.AccountID != work.ID {
			t.Fatalf("anthropic range leaked account %d", snap.AccountID)
		}
	}

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

	cycles, err := s.QueryAnthropicCycleHistoryForAccount(work.ID, "five_hour", 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(cycles) != 200 {
		t.Fatalf("anthropic cycles: got %d, want 200", len(cycles))
	}
	for _, cycle := range cycles {
		if cycle.AccountID != work.ID {
			t.Fatalf("anthropic cycle leaked account %d", cycle.AccountID)
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
