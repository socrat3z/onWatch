package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
	"github.com/onllm-dev/onwatch/v2/internal/notify"
	"github.com/onllm-dev/onwatch/v2/internal/store"
	"github.com/onllm-dev/onwatch/v2/internal/tracker"
)

// TestCodexAgent_SendStarterPing_HitsEndpoint verifies the agent sends a real
// request to the Codex Responses endpoint with the bearer token, account id, and
// a body that asks the model to reply "Quota Resumed".
func TestCodexAgent_SendStarterPing_HitsEndpoint(t *testing.T) {
	type capture struct {
		auth, account, body string
	}
	hit := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hit <- capture{auth: r.Header.Get("Authorization"), account: r.Header.Get("ChatGPT-Account-ID"), body: string(b)}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)

	ag.sendStarterPing(context.Background(), "acct_123", []string{"five_hour"}, nil)

	select {
	case c := <-hit:
		if c.auth != "Bearer tok_abc" {
			t.Errorf("Authorization = %q, want Bearer tok_abc", c.auth)
		}
		if c.account != "acct_123" {
			t.Errorf("ChatGPT-Account-ID = %q, want acct_123", c.account)
		}
		if !strings.Contains(c.body, "Quota Resumed") {
			t.Errorf("request body does not ask for \"Quota Resumed\": %s", c.body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("starter ping did not reach the endpoint")
	}
}

// TestCodexAgent_MaybeAutoStart_RateCap verifies at most codexStarterMaxFires
// pings fire per account within the rate window, whichever windows they start:
// one ping starts every window.
func TestCodexAgent_MaybeAutoStart_RateCap(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetAutoStartCheck(func(string) bool { return true })

	// A start that never takes: every comparable pair of polls retries,
	// alternating the window so the cap is shown to be per account.
	t0 := time.Now().UTC()
	for i := 0; i < 4*codexStarterMaxFires; i++ {
		now := t0.Add(time.Duration(i) * time.Minute)
		name := []string{"five_hour", "seven_day"}[i/4%2]
		full := time.Duration(codexAutoStartWindowSeconds(name)) * time.Second
		autoStartPoll(t, ag, now, map[string]time.Time{name: now.Add(full)})
	}
	if got := atomic.LoadInt32(&count); got != codexStarterMaxFires {
		t.Errorf("endpoint hit %d times, want %d (cap per account)", got, codexStarterMaxFires)
	}
}

func TestIsUnstartedCodexWindow(t *testing.T) {
	now := time.Now().UTC()
	mk := func(d time.Duration) *time.Time { ts := now.Add(d); return &ts }

	cases := []struct {
		name     string
		quota    string
		resetsAt *time.Time
		want     bool
	}{
		{"5h pinned at full = unstarted", "five_hour", mk(5 * time.Hour), true},
		{"5h just under full = unstarted", "five_hour", mk(5*time.Hour - 90*time.Second), true},
		{"5h decreased = started", "five_hour", mk(5*time.Hour - 10*time.Minute), false},
		{"weekly at full = unstarted", "seven_day", mk(7 * 24 * time.Hour), true},
		{"weekly decreased = started", "seven_day", mk(5 * 24 * time.Hour), false},
		{"code_review never managed", "code_review", mk(5 * time.Hour), false},
		{"nil reset", "five_hour", nil, false},
		{"already past", "five_hour", mk(-time.Minute), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnstartedCodexWindow(tc.quota, tc.resetsAt, now); got != tc.want {
				t.Errorf("isUnstartedCodexWindow(%s) = %v, want %v", tc.quota, got, tc.want)
			}
		})
	}
}

// TestCodexAgent_ResetRolling verifies a window counts as rolling only when its
// reset time moved forward with the clock between two comparable polls.
func TestCodexAgent_ResetRolling(t *testing.T) {
	ag := NewCodexAgent(api.NewCodexClient("tok", slog.Default()), nil, nil, time.Hour, slog.Default(), nil)
	t0 := time.Now().UTC()
	at := func(d time.Duration) *time.Time { ts := t0.Add(d); return &ts }

	// seen records an observation fetched at t0+d with the given generation.
	seen := func(name string, reset *time.Time, d time.Duration, gen uint64) bool {
		return ag.resetRolling(name, reset, codexStarterPoll{fetchedAt: t0.Add(d), gen: gen}, gen%2 == 0)
	}
	const week = 7 * 24 * time.Hour

	if seen("five_hour", at(5*time.Hour), 0, 0) {
		t.Error("first observation reported rolling, want false")
	}
	if !seen("five_hour", at(time.Minute+5*time.Hour+time.Second), time.Minute, 0) {
		t.Error("reset moved with the clock (1s rounding), want rolling")
	}
	if seen("five_hour", at(time.Minute+5*time.Hour+time.Second), 2*time.Minute, 0) {
		t.Error("reset fixed, want not rolling")
	}
	if seen("five_hour", at(3*time.Minute+5*time.Hour), 3*time.Minute, 2) {
		t.Error("a ping was claimed and finished between the polls, want not rolling")
	}
	if !seen("five_hour", at(4*time.Minute+5*time.Hour), 4*time.Minute, 2) {
		t.Error("rolling again after a comparable pair, want rolling")
	}
	if seen("five_hour", at(4*time.Minute+3*time.Second+5*time.Hour), 4*time.Minute+3*time.Second, 2) {
		t.Error("polls closer than codexRollingMinGap reported rolling, want false")
	}
	if seen("five_hour", at(5*time.Minute+5*time.Hour), 5*time.Minute, 3) {
		t.Error("ping in flight at the poll, want not rolling")
	}
	if seen("five_hour", nil, 6*time.Minute, 4) {
		t.Error("nil reset reported rolling")
	}
	if seen("five_hour", at(7*time.Minute+5*time.Hour), 7*time.Minute, 4) {
		t.Error("observation after a nil reset reported rolling, want a fresh start")
	}

	// Started by the user 8s before a poll 60s after the last: moved, but not
	// with the clock.
	seen("seven_day", at(week), 0, 4)
	if seen("seven_day", at(52*time.Second+week), time.Minute, 4) {
		t.Error("window started between polls reported rolling, want false")
	}

	// A ping finishing during the fetch makes the poll not comparable even
	// though the generation read before the fetch matched.
	seen("five_hour", at(10*time.Minute+5*time.Hour), 10*time.Minute, 4)
	if ag.resetRolling("five_hour", at(11*time.Minute+5*time.Hour), codexStarterPoll{fetchedAt: t0.Add(11 * time.Minute), gen: 4}, false) {
		t.Error("generation changed during the fetch, want not rolling")
	}
}

// TestCodexAgent_Poll_FiresStarterOnUnstartedWindow is the end-to-end runtime
// check: real polls against a usage endpoint reporting an unstarted 5h window
// (reset_at rolling forward each poll) must drive a starter ping to the
// Responses endpoint with the configured account id. This exercises the same
// path the daemon runs (poll -> maybeAutoStartWindows -> sendStarterPing).
func TestCodexAgent_Poll_FiresStarterOnUnstartedWindow(t *testing.T) {
	var starterHits atomic.Int32
	gotAccount := make(chan string, 4)
	starter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		starterHits.Add(1)
		select {
		case gotAccount <- r.Header.Get("ChatGPT-Account-ID"):
		default:
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer starter.Close()

	// Usage endpoint reports an UNSTARTED 5h window: reset_at rolls to now + 5h.
	usage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reset := time.Now().Unix() + 18000
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":1,"reset_at":%d,"limit_window_seconds":18000}}}`, reset)
	}))
	defer usage.Close()

	st, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer st.Close()

	logger := slog.Default()
	client := api.NewCodexClient("oauth_token", logger,
		api.WithCodexBaseURL(usage.URL), api.WithCodexStarterURL(starter.URL))
	tr := tracker.NewCodexTracker(st, logger)
	ag := NewCodexAgent(client, st, tr, 50*time.Millisecond, logger, nil)
	ag.SetCodexAccountID("acct_e2e")
	ag.SetAutoStartCheck(func(string) bool { return true })
	// Stand in for a poll 6s earlier, since real polls this close together
	// are under codexRollingMinGap.
	earlier := time.Now().Add(-6 * time.Second)
	ag.starterSeen = map[string]codexResetObservation{"five_hour": {
		resetsAt: time.Unix(earlier.Unix()+18000, 0),
		at:       earlier.UTC(),
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	go ag.Run(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	if got := starterHits.Load(); got < 1 {
		t.Fatalf("starter endpoint hit %d times, want >=1 (unstarted window must fire)", got)
	}
	if got := starterHits.Load(); int(got) > codexStarterMaxFires {
		t.Errorf("starter fired %d times, exceeds rate cap %d", got, codexStarterMaxFires)
	}
	select {
	case acct := <-gotAccount:
		if acct != "acct_e2e" {
			t.Errorf("ChatGPT-Account-ID = %q, want acct_e2e", acct)
		}
	default:
		t.Error("no account id captured")
	}
}

// waitStarterIdle waits until no starter ping is in flight.
func waitStarterIdle(t *testing.T, ag *CodexAgent) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ag.starterMu.Lock()
		pending := ag.starterGen%2 == 1
		ag.starterMu.Unlock()
		if !pending {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("starter ping still in flight")
}

// waitDelivered waits for at least want webhook deliveries, which follow a
// ping's release, then briefly for any extra ones, and returns them all.
func waitDelivered(t *testing.T, delivered func() []string, want int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(delivered()) < want && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	return delivered()
}

// autoStartPoll feeds one poll's quotas to maybeAutoStartWindows at now and
// waits for any ping it fired. resets maps window name to its reset time.
func autoStartPoll(t *testing.T, ag *CodexAgent, now time.Time, resets map[string]time.Time) {
	t.Helper()
	sp := codexStarterPoll{fetchedAt: now, gen: ag.starterGeneration()}
	var quotas []api.CodexQuota
	for _, name := range []string{"five_hour", "seven_day"} {
		if reset, ok := resets[name]; ok {
			quotas = append(quotas, api.CodexQuota{Name: name, ResetsAt: &reset})
		}
	}
	ag.maybeAutoStartWindows(context.Background(), quotas, now, sp)
	waitStarterIdle(t, ag)
}

// TestCodexAgent_MaybeAutoStart fires only for windows that are both enabled and
// observed unstarted.
func TestCodexAgent_MaybeAutoStart(t *testing.T) {
	var count int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&count, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetCodexAccountID("acct")

	t0 := time.Now().UTC()
	sevenStarted := t0.Add(2 * 24 * time.Hour) // started (well below full)
	poll := func(now time.Time) {
		autoStartPoll(t, ag, now, map[string]time.Time{
			"five_hour": now.Add(5 * time.Hour), // unstarted, rolling
			"seven_day": sevenStarted,
		})
	}

	// Disabled: nothing fires.
	ag.SetAutoStartCheck(func(string) bool { return false })
	poll(t0)
	poll(t0.Add(time.Minute))
	if got := atomic.LoadInt32(&count); got != 0 {
		t.Fatalf("disabled auto-start fired %d times, want 0", got)
	}

	// Enabled: the rolling five_hour fires at once, since the disabled polls
	// already recorded its reset time.
	var asked []string
	ag.SetAutoStartCheck(func(q string) bool { asked = append(asked, q); return true })
	poll(t0.Add(2 * time.Minute))
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Errorf("enabled auto-start fired %d times, want 1 (only unstarted five_hour)", got)
	}
	if len(asked) != 1 || asked[0] != "five_hour" {
		t.Errorf("toggle checked for %v, want only five_hour", asked)
	}
}

// The issue #129 sequence: a weekly window started by a ping still shows nearly
// the full countdown on the next poll, so it looked unstarted and got a second
// ping and a second success notification. A fixed reset time must not fire,
// and neither may a restarted agent that first sees the window in that state.
func TestCodexAgent_MaybeAutoStart_OnePingPerWindowStart(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	newAgent := func() *CodexAgent {
		ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
		ag.SetNotifier(engine)
		ag.SetAutoStartCheck(func(string) bool { return true })
		return ag
	}
	ag := newAgent()

	const week = 7 * 24 * time.Hour
	t0 := time.Now().UTC()
	started := t0.Add(2*time.Minute + week) // anchored by the ping at t0+2m

	autoStartPoll(t, ag, t0, map[string]time.Time{"seven_day": t0.Add(week)})
	autoStartPoll(t, ag, t0.Add(2*time.Minute), map[string]time.Time{"seven_day": t0.Add(2*time.Minute + week)})
	if got := count.Load(); got != 1 {
		t.Fatalf("starter fired %d times on the unstarted window, want 1", got)
	}
	// Polls 10s apart up to 1m58s after the ping: still inside the unstarted
	// band, but the reset time is fixed.
	for gap := 108 * time.Second; gap <= 118*time.Second; gap += 10 * time.Second {
		autoStartPoll(t, ag, t0.Add(2*time.Minute+gap), map[string]time.Time{"seven_day": started})
	}
	if got := count.Load(); got != 1 {
		t.Errorf("starter fired %d times after the window started, want 1", got)
	}
	if events := waitDelivered(t, delivered, 1); len(events) != 1 || events[0] != notify.EventStarterSuccess {
		t.Errorf("delivered events = %v, want one %s", events, notify.EventStarterSuccess)
	}

	restarted := newAgent()
	autoStartPoll(t, restarted, t0.Add(3*time.Minute), map[string]time.Time{"seven_day": started})
	autoStartPoll(t, restarted, t0.Add(3*time.Minute+30*time.Second), map[string]time.Time{"seven_day": started})
	if got := count.Load(); got != 1 {
		t.Errorf("restarted agent fired on a started window: %d pings, want 1", got)
	}
}

// A failed ping leaves the window rolling, so a later poll retries it. The poll
// right after the ping is not comparable with the one before it.
func TestCodexAgent_MaybeAutoStart_RetriesFailedStart(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetAutoStartCheck(func(string) bool { return true })

	t0 := time.Now().UTC()
	for i := 0; i < 4; i++ {
		now := t0.Add(time.Duration(i) * time.Minute)
		autoStartPoll(t, ag, now, map[string]time.Time{"five_hour": now.Add(5 * time.Hour)})
	}
	if got := count.Load(); got != 2 {
		t.Errorf("starter fired %d times over four polls of a failing start, want 2", got)
	}
}

// One turn starts every window, so two unstarted windows get one ping, with a
// success notification for each window.
func TestCodexAgent_MaybeAutoStart_OnePingForBothWindows(t *testing.T) {
	var count atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)
	ag.SetAutoStartCheck(func(string) bool { return true })

	t0 := time.Now().UTC()
	for i := 0; i < 2; i++ {
		now := t0.Add(time.Duration(i) * time.Minute)
		autoStartPoll(t, ag, now, map[string]time.Time{
			"five_hour": now.Add(5 * time.Hour),
			"seven_day": now.Add(7 * 24 * time.Hour),
		})
	}
	if got := count.Load(); got != 1 {
		t.Errorf("starter fired %d times for two unstarted windows, want 1", got)
	}
	if events := waitDelivered(t, delivered, 2); len(events) != 2 {
		t.Errorf("delivered events = %v, want one success per window", events)
	}
}

// A poll that lands while a ping is still in flight must not fire another.
func TestCodexAgent_MaybeAutoStart_SkipsWhilePingInFlight(t *testing.T) {
	var count atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := api.NewCodexClient("tok", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetAutoStartCheck(func(string) bool { return true })

	t0 := time.Now().UTC()
	poll := func(now time.Time) {
		reset := now.Add(5 * time.Hour)
		sp := codexStarterPoll{fetchedAt: now, gen: ag.starterGeneration()}
		ag.maybeAutoStartWindows(context.Background(), []api.CodexQuota{{Name: "five_hour", ResetsAt: &reset}}, now, sp)
	}
	poll(t0)
	poll(t0.Add(time.Minute))
	<-entered
	poll(t0.Add(2 * time.Minute))
	close(release)
	waitStarterIdle(t, ag)

	if got := count.Load(); got != 1 {
		t.Errorf("starter fired %d times with a ping in flight, want 1", got)
	}
}

// starterWebhookEngine wires a notification engine whose webhook channel points
// at a capture server, so starter events can be observed end to end.
func starterWebhookEngine(t *testing.T) (*notify.NotificationEngine, func() []string) {
	t.Helper()

	var mu sync.Mutex
	var events []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p notify.WebhookPayload
		json.NewDecoder(r.Body).Decode(&p)
		mu.Lock()
		events = append(events, p.Event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(hook.Close)

	db, err := store.New(":memory:")
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })

	webhookJSON, _ := json.Marshal(map[string]interface{}{
		"url":             hook.URL,
		"timeout_seconds": 2,
		"events":          map[string]bool{"starter_success": true, "starter_failure": true},
	})
	db.SetSetting("webhook", string(webhookJSON))
	notifJSON, _ := json.Marshal(map[string]interface{}{
		"warning_threshold":  80,
		"critical_threshold": 95,
		"channels":           map[string]bool{"webhook": true},
	})
	db.SetSetting("notifications", string(notifJSON))

	engine := notify.New(db, slog.Default())
	if err := engine.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if err := engine.ConfigureWebhook(); err != nil {
		t.Fatalf("ConfigureWebhook() error = %v", err)
	}

	return engine, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), events...)
	}
}

// A successful starter ping notifies the webhook channel.
func TestCodexAgent_SendStarterPing_NotifiesOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	ag.sendStarterPing(context.Background(), "acct_123", []string{"five_hour"}, nil)

	events := delivered()
	if len(events) != 1 || events[0] != notify.EventStarterSuccess {
		t.Errorf("delivered events = %v, want [%s]", events, notify.EventStarterSuccess)
	}
}

// A failed starter ping notifies the webhook channel with the failure event.
func TestCodexAgent_SendStarterPing_NotifiesOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	ag.sendStarterPing(context.Background(), "acct_123", []string{"five_hour"}, nil)

	events := delivered()
	if len(events) != 1 || events[0] != notify.EventStarterFailure {
		t.Errorf("delivered events = %v, want [%s]", events, notify.EventStarterFailure)
	}
}

// A ping skipped by the rate cap is not a delivery outcome and must stay silent.
func TestCodexAgent_MaybeAutoStart_RateCappedSendsNoEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: response.completed\n\n"))
	}))
	defer srv.Close()

	engine, delivered := starterWebhookEngine(t)
	client := api.NewCodexClient("tok_abc", slog.Default(), api.WithCodexStarterURL(srv.URL))
	ag := NewCodexAgent(client, nil, nil, time.Hour, slog.Default(), nil)
	ag.SetNotifier(engine)

	ag.SetAutoStartCheck(func(string) bool { return true })

	// The window keeps rolling, so polls keep retrying until the cap.
	t0 := time.Now().UTC()
	for i := 0; i < 4*codexStarterMaxFires; i++ {
		now := t0.Add(time.Duration(i) * time.Minute)
		autoStartPoll(t, ag, now, map[string]time.Time{"five_hour": now.Add(5 * time.Hour)})
	}

	if got := len(waitDelivered(t, delivered, codexStarterMaxFires)); got != codexStarterMaxFires {
		t.Errorf("delivered %d events, want %d (rate-capped pings send nothing)", got, codexStarterMaxFires)
	}
}
