package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type managerTestRunner struct {
	started chan struct{}
	stopped chan struct{}
}

func newManagerTestRunner() *managerTestRunner {
	return &managerTestRunner{
		started: make(chan struct{}, 1),
		stopped: make(chan struct{}, 1),
	}
}

func (r *managerTestRunner) Run(ctx context.Context) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	select {
	case r.stopped <- struct{}{}:
	default:
	}
	return nil
}

func TestAgentManager_StartStop(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	runner := newManagerTestRunner()
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) { return runner, nil })

	if err := mgr.Start("synthetic"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}

	if !mgr.IsRunning("synthetic") {
		t.Fatal("expected synthetic to be running")
	}

	mgr.Stop("synthetic")
	select {
	case <-runner.stopped:
	case <-time.After(time.Second):
		t.Fatal("runner did not stop")
	}

	if mgr.IsRunning("synthetic") {
		t.Fatal("expected synthetic to be stopped")
	}
}

func TestAgentManager_StartUnknown(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	if err := mgr.Start("missing"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestAgentManager_StartFactoryError(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) {
		return nil, errors.New("boom")
	})
	if err := mgr.Start("synthetic"); err == nil {
		t.Fatal("expected factory error")
	}
}

func TestAgentManager_StopAll(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	r1 := newManagerTestRunner()
	r2 := newManagerTestRunner()
	mgr.RegisterFactory("synthetic", func() (AgentRunner, error) { return r1, nil })
	mgr.RegisterFactory("zai", func() (AgentRunner, error) { return r2, nil })

	if err := mgr.Start("synthetic"); err != nil {
		t.Fatalf("Start synthetic: %v", err)
	}
	if err := mgr.Start("zai"); err != nil {
		t.Fatalf("Start zai: %v", err)
	}

	select {
	case <-r1.started:
	case <-time.After(time.Second):
		t.Fatal("synthetic not started")
	}
	select {
	case <-r2.started:
	case <-time.After(time.Second):
		t.Fatal("zai not started")
	}

	mgr.StopAll()
	select {
	case <-r1.stopped:
	case <-time.After(time.Second):
		t.Fatal("synthetic not stopped")
	}
	select {
	case <-r2.stopped:
	case <-time.After(time.Second):
		t.Fatal("zai not stopped")
	}

	if mgr.IsRunning("synthetic") || mgr.IsRunning("zai") {
		t.Fatal("expected all runners stopped")
	}
}

// A runner whose Run does not return promptly after cancellation. Stop must
// wait for it (bounded) so a restart cannot overlap two copies of the agent.
type slowExitRunner struct {
	started chan struct{}
	release chan struct{}
	exited  chan struct{}
}

func newSlowExitRunner() *slowExitRunner {
	return &slowExitRunner{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
		exited:  make(chan struct{}),
	}
}

func (r *slowExitRunner) Run(ctx context.Context) error {
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	<-r.release
	close(r.exited)
	return nil
}

// Stop must not return until the Run goroutine has exited, even when the
// runner is slow to unwind. Without the wait a restart would run a second Run
// loop over the same shared instance.
func TestAgentManager_StopWaitsForRunExit(t *testing.T) {
	mgr := NewAgentManager(slog.Default())
	runner := newSlowExitRunner()
	mgr.RegisterFactory("codex", func() (AgentRunner, error) { return runner, nil })

	if err := mgr.Start("codex"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-runner.started

	stopped := make(chan struct{})
	go func() {
		mgr.Stop("codex")
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned before the Run goroutine exited")
	case <-time.After(30 * time.Millisecond):
	}

	close(runner.release)
	select {
	case <-runner.exited:
	case <-time.After(time.Second):
		t.Fatal("runner did not exit")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after Run exited")
	}
}

// Stop is bounded: a runner that never returns must not hang the caller, and
// the warning must be logged.
func TestAgentManager_StopBoundedWhenRunStuck(t *testing.T) {
	prev := stopWaitTimeout
	stopWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { stopWaitTimeout = prev })

	var logs bytes.Buffer
	mgr := NewAgentManager(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	mgr.RegisterFactory("codex", func() (AgentRunner, error) {
		return agentRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			select {} // never returns
		}), nil
	})

	if err := mgr.Start("codex"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan struct{})
	go func() {
		mgr.Stop("codex")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not honour the bounded timeout")
	}
	if !strings.Contains(logs.String(), "did not exit within the stop timeout") {
		t.Fatalf("expected a timeout warning, logs=%q", logs.String())
	}
	if mgr.IsRunning("codex") {
		t.Fatal("agent must not be considered running after Stop")
	}
}

type agentRunnerFunc func(ctx context.Context) error

func (f agentRunnerFunc) Run(ctx context.Context) error { return f(ctx) }

// overlapRunner counts how many copies of Run are active at once, so a test can
// prove Stop never leaves a window where Start runs a second copy alongside the
// first.
type overlapRunner struct {
	mu        sync.Mutex
	active    int
	maxActive int
	started   chan struct{}
	release   chan struct{}
}

func newOverlapRunner() *overlapRunner {
	return &overlapRunner{started: make(chan struct{}, 8), release: make(chan struct{})}
}

func (r *overlapRunner) Run(ctx context.Context) error {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	select {
	case r.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	<-r.release
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return nil
}

func (r *overlapRunner) max() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxActive
}

// HIGH: Stop must set the stopping marker in the SAME locked section that
// removes the key from running. Otherwise a Start landing in the gap between
// the delete and beginStopping sees neither map and starts a second copy of the
// shared Anthropic/Codex instance while the old Run is still active - which can
// drive concurrent OAuth refreshes and log the user out of Claude Code.
func TestAgentManager_StopHasNoStartWindow(t *testing.T) {
	prev := stopWaitTimeout
	stopWaitTimeout = time.Second
	t.Cleanup(func() { stopWaitTimeout = prev })

	mgr := NewAgentManager(slog.Default())
	runner := newOverlapRunner()
	mgr.RegisterFactory("anthropic", func() (AgentRunner, error) { return runner, nil })

	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-runner.started
	// Let both runs unwind as soon as they observe cancellation.
	close(runner.release)

	// Deterministic interleaving: run a Start exactly in the window right after
	// Stop removes the key from running and releases the lock.
	testHookStopAfterRemove = func(key string) {
		if key != "anthropic" {
			return
		}
		if err := mgr.Start("anthropic"); err == nil {
			// Wait for the second copy to actually enter Run, so an overlap is
			// observed if one exists.
			select {
			case <-runner.started:
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	t.Cleanup(func() { testHookStopAfterRemove = nil })

	mgr.Stop("anthropic")

	if got := runner.max(); got > 1 {
		t.Fatalf("two copies ran concurrently (max=%d); Stop left a start window", got)
	}
}

// After a Stop times out, the old Run is still unwinding. A Start must be queued
// (not run alongside the old copy) and must run once the old copy exits.
func TestAgentManager_RefusedStartRunsAfterExit(t *testing.T) {
	prev := stopWaitTimeout
	stopWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { stopWaitTimeout = prev })

	mgr := NewAgentManager(slog.Default())
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	var runs atomic.Int32
	mgr.RegisterFactory("anthropic", func() (AgentRunner, error) {
		return agentRunnerFunc(func(ctx context.Context) error {
			runs.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			<-release // unwinds only when the test allows it
			return nil
		}), nil
	})

	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	// Stop times out while the old Run is still unwinding.
	mgr.Stop("anthropic")
	if !mgr.IsStopping("anthropic") {
		t.Fatal("expected the key to be stopping after a timed-out Stop")
	}

	// The refused Start is queued, not dropped.
	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("queued Start returned an error: %v", err)
	}
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1 (queued start must not run alongside the old copy)", got)
	}

	// Once the old Run exits, the queued start runs on its own.
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for runs.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("queued start never ran after the old Run exited")
		}
		time.Sleep(time.Millisecond)
	}
	mgr.StopAll()
}

// A later Stop cancels a queued start: the provider must not come back up.
func TestAgentManager_StopCancelsPendingStart(t *testing.T) {
	prev := stopWaitTimeout
	stopWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { stopWaitTimeout = prev })

	mgr := NewAgentManager(slog.Default())
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	var runs atomic.Int32
	mgr.RegisterFactory("anthropic", func() (AgentRunner, error) {
		return agentRunnerFunc(func(ctx context.Context) error {
			runs.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			<-release
			return nil
		}), nil
	})

	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-started
	mgr.Stop("anthropic") // times out, key stopping
	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("queued Start: %v", err)
	}
	// A second Stop cancels the queued start.
	mgr.Stop("anthropic")

	close(release)
	// Give any stray start a chance to run.
	time.Sleep(100 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want 1 (the queued start must be cancelled)", got)
	}
}

// StopAll must use one shared deadline for every agent, not the per-agent
// timeout applied sequentially. Two stuck agents must not take 2x the timeout.
func TestAgentManager_StopAllUsesSharedDeadline(t *testing.T) {
	prev := stopWaitTimeout
	stopWaitTimeout = 300 * time.Millisecond
	t.Cleanup(func() { stopWaitTimeout = prev })

	mgr := NewAgentManager(slog.Default())
	stuck := func() (AgentRunner, error) {
		return agentRunnerFunc(func(ctx context.Context) error {
			<-ctx.Done()
			select {} // never returns
		}), nil
	}
	mgr.RegisterFactory("anthropic", stuck)
	mgr.RegisterFactory("codex", stuck)

	if err := mgr.Start("anthropic"); err != nil {
		t.Fatalf("Start anthropic: %v", err)
	}
	if err := mgr.Start("codex"); err != nil {
		t.Fatalf("Start codex: %v", err)
	}

	start := time.Now()
	mgr.StopAll()
	elapsed := time.Since(start)
	// One shared deadline (~300ms), not two sequential ones (~600ms).
	if elapsed > 550*time.Millisecond {
		t.Fatalf("StopAll took %v; expected one shared deadline (~%v)", elapsed, stopWaitTimeout)
	}
}

func TestAgentManager_RegisteredProviders(t *testing.T) {
	t.Parallel()
	mgr := NewAgentManager(slog.Default())
	mgr.RegisterFactory("zai", func() (AgentRunner, error) { return newManagerTestRunner(), nil })
	mgr.RegisterFactory("anthropic", func() (AgentRunner, error) { return newManagerTestRunner(), nil })
	mgr.RegisterFactory("codex", func() (AgentRunner, error) { return newManagerTestRunner(), nil })

	got := mgr.RegisteredProviders()
	want := []string{"anthropic", "codex", "zai"}
	if len(got) != len(want) {
		t.Fatalf("RegisteredProviders length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RegisteredProviders[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}
