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
