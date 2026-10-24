package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onllm-dev/onwatch/v2/internal/api"
)

type managedRetryRunner struct {
	started, release, exited chan struct{}
	retries                  atomic.Int32
}

func (r *managedRetryRunner) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	<-r.release
	close(r.exited)
	return nil
}
func (r *managedRetryRunner) RequestRetry() error { r.retries.Add(1); return nil }
func (r *managedRetryRunner) ConnectionState() api.ProviderConnection {
	return api.ProviderConnection{CanRetry: true}
}

func TestAgentManagerRetryUsesCurrentInstance(t *testing.T) {
	m := NewAgentManager(nil)
	makeRunner := func() *managedRetryRunner {
		return &managedRetryRunner{started: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{})}
	}
	old, current := makeRunner(), makeRunner()
	m.RegisterFactory("mistral", func() (AgentRunner, error) { return old, nil })
	if err := m.RequestRetry("mistral"); !errors.Is(err, ErrRetryUnavailable) {
		t.Fatal(err)
	}
	if err := m.Start("mistral"); err != nil {
		t.Fatal(err)
	}
	<-old.started
	if err := m.RequestRetry("mistral"); err != nil {
		t.Fatal(err)
	}

	// Stop drains the old Run before returning, so the replacement never
	// overlaps it. Release the old run so Stop can complete.
	stopped := make(chan struct{})
	go func() { m.Stop("mistral"); close(stopped) }()
	close(old.release)
	<-old.exited
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the old Run exited")
	}
	if m.IsStopping("mistral") {
		t.Fatal("stopping entry was not cleared")
	}

	m.RegisterFactory("mistral", func() (AgentRunner, error) { return current, nil })
	if err := m.Start("mistral"); err != nil {
		t.Fatal(err)
	}
	<-current.started
	defer func() { m.StopAll(); close(current.release); <-current.exited }()

	// Retries route to the current instance, not the drained one.
	if err := m.RequestRetry("mistral"); err != nil {
		t.Fatal(err)
	}
	if old.retries.Load() != 1 || current.retries.Load() == 0 {
		t.Fatal("retry routed to stale instance")
	}
	if c, ok := m.ConnectionState("mistral"); !ok || !c.CanRetry {
		t.Fatal("missing running connection state")
	}
}
