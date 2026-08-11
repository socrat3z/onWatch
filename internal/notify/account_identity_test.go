package notify

import (
	"fmt"
	"strings"
	"testing"
)

func TestNotificationKeyKeepsTwoAccountsIndependent(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	engine := newTestEngine(t, s)

	work, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatalf("create work account: %v", err)
	}
	personal, err := s.CreateOrRestoreProviderAccount("anthropic", "personal")
	if err != nil {
		t.Fatalf("create personal account: %v", err)
	}

	workKey := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: fmt.Sprintf("%d", work.ID)})
	personalKey := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: fmt.Sprintf("%d", personal.ID)})
	if workKey == personalKey {
		t.Fatalf("two accounts must not share an alert state, both keyed %q", workKey)
	}
}

func TestNotificationKeyIsStableAcrossAmbientAndManagerModes(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	engine := newTestEngine(t, s)

	def, err := s.EnsureDefaultProviderAccount("anthropic")
	if err != nil {
		t.Fatalf("EnsureDefaultProviderAccount: %v", err)
	}

	ambient := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: "0"})
	managed := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: fmt.Sprintf("%d", def.ID)})
	unset := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h"})

	if ambient != managed || ambient != unset {
		t.Fatalf("switching to manager mode must not re-alert: ambient=%q managed=%q unset=%q", ambient, managed, unset)
	}
	if ambient != "5h" {
		t.Fatalf("the default account must keep the historical bare key, got %q", ambient)
	}
}

func TestNotificationKeyDoesNotAssumeAccountOneIsDefault(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	engine := newTestEngine(t, s)

	// Give "anthropic" a default row and a named row; whichever ID the named
	// account gets, it must never collapse onto the default's bare key.
	if _, err := s.EnsureDefaultProviderAccount("anthropic"); err != nil {
		t.Fatalf("EnsureDefaultProviderAccount: %v", err)
	}
	named, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatalf("create named account: %v", err)
	}

	key := engine.notificationQuotaKey(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: fmt.Sprintf("%d", named.ID)})
	if key == "5h" {
		t.Fatal("a named account must not inherit the default account's alert state")
	}
	if !strings.HasPrefix(key, fmt.Sprintf("%d:", named.ID)) {
		t.Fatalf("unexpected key %q", key)
	}
}

func TestAlertBodiesNameTheAccountAlias(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	engine := newTestEngine(t, s)

	account, err := s.CreateOrRestoreProviderAccount("anthropic", "work")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := s.UpdateProviderAccountAlias("anthropic", account.ID, "Work laptop"); err != nil {
		t.Fatalf("UpdateProviderAccountAlias: %v", err)
	}
	id := fmt.Sprintf("%d", account.ID)

	quotaBody := engine.buildBody(QuotaStatus{Provider: "anthropic", QuotaKey: "5h", AccountID: id, Utilization: 96}, "critical")
	if !strings.Contains(quotaBody, "Account: Work laptop") {
		t.Fatalf("quota alert must name the alias, got:\n%s", quotaBody)
	}
	if strings.Contains(quotaBody, "Account: "+id) {
		t.Fatalf("quota alert must not print a raw row ID, got:\n%s", quotaBody)
	}

	authBody := engine.buildAuthErrorBody(AuthErrorAlert{Provider: "anthropic", Title: "Token expired", Message: "re-login", AccountID: id})
	if !strings.Contains(authBody, "Account: Work laptop") {
		t.Fatalf("auth alert must name the alias, got:\n%s", authBody)
	}
}

func TestAccountLabelIgnoresAnotherProvidersAccount(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()
	engine := newTestEngine(t, s)

	other, err := s.CreateOrRestoreProviderAccount("antigravity", "work")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	id := fmt.Sprintf("%d", other.ID)
	if label := engine.accountLabel("anthropic", id); label != id {
		t.Fatalf("a cross-provider ID must not resolve to that account's alias, got %q", label)
	}
}
