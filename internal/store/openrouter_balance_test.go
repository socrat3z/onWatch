package store

import (
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

func TestOpenRouterSnapshotPersistsAccountBalance(t *testing.T) {
	t.Parallel()

	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	totalCredits, accountUsage, accountBalance := 100.5, 25.75, 74.75
	_, err = s.InsertOpenRouterSnapshot(&api.OpenRouterSnapshot{
		CapturedAt:     time.Now().UTC(),
		AccountCredits: &totalCredits,
		AccountUsage:   &accountUsage,
		AccountBalance: &accountBalance,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.QueryLatestOpenRouter()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccountBalance == nil || *got.AccountBalance != accountBalance {
		t.Fatalf("account balance = %v, want %v", got.AccountBalance, accountBalance)
	}
}
