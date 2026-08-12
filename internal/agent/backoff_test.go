package agent

import "testing"

func TestBackoffCycles_Calculation(t *testing.T) {
	tests := []struct {
		failCount int
		want      int
	}{
		{0, backoffBaseCycles}, // 1
		{1, 1},                 // 1 * 2^0 = 1
		{2, 2},                 // 1 * 2^1 = 2
		{3, 4},                 // 1 * 2^2 = 4
		{4, 8},                 // 1 * 2^3 = 8
		{5, backoffMaxCycles},  // 1 * 2^4 = 16 -> capped at 10
		{10, backoffMaxCycles},
		{20, backoffMaxCycles},
	}
	for _, tt := range tests {
		if got := backoffCycles(tt.failCount); got != tt.want {
			t.Errorf("backoffCycles(%d) = %d, want %d", tt.failCount, got, tt.want)
		}
	}
}

func TestPollBackoff_RateLimited_ArmsSkip(t *testing.T) {
	var b pollBackoff

	if n := b.RateLimited(); n != 1 {
		t.Errorf("first RateLimited() = %d, want 1", n)
	}
	if !b.ShouldSkip() {
		t.Error("expected ShouldSkip() true after first failure")
	}
	if b.ShouldSkip() {
		t.Error("expected ShouldSkip() false after skip budget consumed")
	}

	if n := b.RateLimited(); n != 1 {
		t.Errorf("second RateLimited() = %d, want 1", n)
	}
	if n := b.RateLimited(); n != 2 {
		t.Errorf("third RateLimited() = %d, want 2", n)
	}
}

func TestPollBackoff_ShouldSkip_NoBackoffArmed(t *testing.T) {
	var b pollBackoff
	if b.ShouldSkip() {
		t.Error("expected ShouldSkip() false with no backoff armed")
	}
}

func TestPollBackoff_Reset(t *testing.T) {
	var b pollBackoff
	b.RateLimited()
	b.RateLimited()

	b.Reset()

	if b.failCount != 0 {
		t.Errorf("failCount = %d, want 0 after Reset", b.failCount)
	}
	if b.skipRemaining != 0 {
		t.Errorf("skipRemaining = %d, want 0 after Reset", b.skipRemaining)
	}
	if b.ShouldSkip() {
		t.Error("expected ShouldSkip() false after Reset")
	}
}

func TestPollBackoff_RateLimited_GrowsAndCaps(t *testing.T) {
	var b pollBackoff
	var got []int
	for i := 0; i < 8; i++ {
		got = append(got, b.RateLimited())
	}
	want := []int{1, 1, 2, 4, 8, backoffMaxCycles, backoffMaxCycles, backoffMaxCycles}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RateLimited() call %d = %d, want %d", i+1, got[i], want[i])
		}
	}
}
