package agent

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// defaultStopWaitTimeout bounds how long Stop waits for a cancelled agent's Run
// goroutine to return before giving up with a warning.
const defaultStopWaitTimeout = 5 * time.Second

// AgentRunner runs a provider polling loop until context cancellation.
type AgentRunner interface {
	Run(ctx context.Context) error
}

// RunnerFactory builds a fresh AgentRunner instance for a provider.
// A factory can return an error when the provider is not currently configurable.
type RunnerFactory func() (AgentRunner, error)

// AgentManager manages dynamic provider agent start/stop lifecycle.
type AgentManager struct {
	mu        sync.RWMutex
	factories map[string]RunnerFactory
	running   map[string]*runningAgent
	// stopping holds agents whose Run goroutine is still unwinding. A key is
	// added in the same locked section that removes it from running, so a Start
	// can never observe a gap and start a second copy. It is cleared once the
	// Run goroutine exits.
	stopping map[string]*runningAgent
	// pending holds a start requested while the key was stopping. It is run
	// once the old Run exits, unless a later Stop cancels it.
	pending map[string]bool
	logger  *slog.Logger
}

// testHookStopAfterRemove, when set, runs right after Stop removes the key from
// running and releases the lock. Tests use it to prove no Start window exists
// between the delete and the stopping marker.
var testHookStopAfterRemove func(key string)

type runningAgent struct {
	cancel context.CancelFunc
	runner AgentRunner
	// done is closed when the agent's Run goroutine returns. Stop waits on it
	// so a restart can never overlap two copies of the same agent.
	done chan struct{}
}

// NewAgentManager creates a new manager.
func NewAgentManager(logger *slog.Logger) *AgentManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &AgentManager{
		factories: make(map[string]RunnerFactory),
		running:   make(map[string]*runningAgent),
		stopping:  make(map[string]*runningAgent),
		pending:   make(map[string]bool),
		logger:    logger,
	}
}

// RegisterFactory registers or replaces the runner factory for a provider key.
func (m *AgentManager) RegisterFactory(key string, factory RunnerFactory) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.factories[key] = factory
}

// RegisteredProviders returns sorted provider keys with registered factories.
func (m *AgentManager) RegisteredProviders() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.factories))
	for key := range m.factories {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Start starts a provider agent if not already running. If the key is still
// stopping (a previous Run has not exited yet), Start records a pending start
// instead of running a second copy, and returns nil. The pending start runs
// once the old Run exits, unless a later Stop cancels it.
func (m *AgentManager) Start(key string) error {
	m.mu.Lock()
	if _, running := m.running[key]; running {
		m.mu.Unlock()
		return nil
	}
	if _, stopping := m.stopping[key]; stopping {
		m.pending[key] = true
		m.mu.Unlock()
		m.logger.Info("agent start deferred until the previous run exits", "provider", key)
		return nil
	}
	factory, ok := m.factories[key]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("agent %s not registered", key)
	}

	runner, err := factory()
	if err != nil {
		return err
	}
	if runner == nil {
		return fmt.Errorf("agent %s factory returned nil runner", key)
	}

	ctx, cancel := context.WithCancel(context.Background())

	m.mu.Lock()
	if _, running := m.running[key]; running {
		m.mu.Unlock()
		cancel()
		return nil
	}
	if _, stopping := m.stopping[key]; stopping {
		// A Stop started while this Start was building the runner. Defer rather
		// than overlap; the pending start runs once the old Run exits.
		m.pending[key] = true
		m.mu.Unlock()
		cancel()
		m.logger.Info("agent start deferred until the previous run exits", "provider", key)
		return nil
	}
	entry := &runningAgent{cancel: cancel, runner: runner, done: make(chan struct{})}
	m.running[key] = entry
	m.mu.Unlock()

	go func() {
		defer close(entry.done)
		m.logger.Info("Starting agent", "provider", key)
		if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
			m.logger.Error("Agent error", "provider", key, "error", err)
		}
		m.mu.Lock()
		if m.running[key] == entry {
			delete(m.running, key)
		}
		m.mu.Unlock()
	}()

	return nil
}

// stopWaitTimeout is the bound Stop waits for a cancelled Run goroutine to
// return. It is a variable so tests can shorten it.
var stopWaitTimeout = defaultStopWaitTimeout

// SetStopWaitTimeoutForTest overrides the stop wait timeout and returns a
// function that restores the previous value. It exists so tests in other
// packages can exercise the timed-out Stop path without waiting the full
// production timeout.
func SetStopWaitTimeoutForTest(d time.Duration) func() {
	prev := stopWaitTimeout
	stopWaitTimeout = d
	return func() { stopWaitTimeout = prev }
}

// waitForExit blocks until the agent's Run goroutine returns or the deadline
// elapses. A timeout is logged but not fatal: Stop must never hang the caller
// (for example the settings save path). It reports whether the goroutine
// actually exited.
func (m *AgentManager) waitForExit(key string, entry *runningAgent, deadline time.Time) bool {
	if entry.done == nil {
		return true
	}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		m.logger.Warn("agent did not exit within the stop timeout", "provider", key, "timeout", stopWaitTimeout)
		return false
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-entry.done:
		return true
	case <-timer.C:
		m.logger.Warn("agent did not exit within the stop timeout", "provider", key, "timeout", stopWaitTimeout)
		return false
	}
}

// endStopping clears the stopping marker for a key whose Run has exited and
// runs any pending start that is still wanted. It is safe to call more than
// once; only the first call for a live stopping entry acts.
func (m *AgentManager) endStopping(key string) {
	m.mu.Lock()
	if _, ok := m.stopping[key]; !ok {
		m.mu.Unlock()
		return
	}
	delete(m.stopping, key)
	restart := m.pending[key]
	delete(m.pending, key)
	m.mu.Unlock()
	if restart {
		m.logger.Info("running deferred agent start", "provider", key)
		if err := m.Start(key); err != nil {
			m.logger.Warn("deferred agent start failed", "provider", key, "error", err)
		}
	}
}

// watchStopping clears the stopping marker from a goroutine once the Run
// goroutine exits, for the case where Stop timed out and cannot clear it
// synchronously. It is idempotent with a synchronous endStopping.
func (m *AgentManager) watchStopping(key string, entry *runningAgent) {
	if entry.done == nil {
		return
	}
	go func() {
		<-entry.done
		m.endStopping(key)
	}()
}

// Stop cancels the running provider agent, if present, and waits (bounded) for
// its Run goroutine to exit before returning. The key is moved from running to
// stopping in the SAME locked section, so a concurrent Start can never see a
// gap and run a second copy alongside the still-unwinding Run. A Stop also
// cancels any pending start, so toggling off after a refused start leaves the
// provider stopped.
func (m *AgentManager) Stop(key string) {
	m.mu.Lock()
	entry, running := m.running[key]
	if running {
		delete(m.running, key)
		if entry.done != nil {
			m.stopping[key] = entry
		}
	}
	// A Stop cancels a queued start: the provider is being switched off.
	delete(m.pending, key)
	m.mu.Unlock()

	if testHookStopAfterRemove != nil {
		testHookStopAfterRemove(key)
	}

	if !running {
		return
	}
	entry.cancel()
	if m.waitForExit(key, entry, time.Now().Add(stopWaitTimeout)) {
		// The Run has exited; clear the marker before returning so a
		// subsequent Start is not deferred.
		m.endStopping(key)
	} else {
		// Still unwinding: let a goroutine clear the marker when it finishes.
		m.watchStopping(key, entry)
	}
	m.logger.Info("Stopped agent", "provider", key)
}

// StopAll stops all running providers and waits (bounded) for each to exit.
// Every agent shares one deadline, so N stuck agents cost one timeout rather
// than N.
func (m *AgentManager) StopAll() {
	m.mu.Lock()
	entries := make([]*runningAgent, 0, len(m.running))
	keys := make([]string, 0, len(m.running))
	for key, entry := range m.running {
		delete(m.running, key)
		if entry.done != nil {
			m.stopping[key] = entry
		}
		keys = append(keys, key)
		entries = append(entries, entry)
	}
	// StopAll switches everything off; drop any queued starts.
	m.pending = make(map[string]bool)
	m.mu.Unlock()

	deadline := time.Now().Add(stopWaitTimeout)
	for _, entry := range entries {
		entry.cancel()
	}
	for i, entry := range entries {
		if m.waitForExit(keys[i], entry, deadline) {
			m.endStopping(keys[i])
		} else {
			m.watchStopping(keys[i], entry)
		}
		m.logger.Info("Stopped agent", "provider", keys[i])
	}
}

// IsRunning returns whether a provider is currently running.
func (m *AgentManager) IsRunning(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, running := m.running[key]
	return running
}

// IsStopping reports whether a provider's previous Run is still unwinding. A
// Start while this is true is deferred, not run alongside the old copy.
func (m *AgentManager) IsStopping(key string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, stopping := m.stopping[key]
	return stopping
}
