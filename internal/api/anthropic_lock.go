package api

import (
	"context"
	"errors"
	"time"
)

// credentialLockTimeout bounds how long a rotation waits for a peer holding the
// credential lock. A `claude auth login` keeps the lock for as long as the
// operator takes to paste a code, so a blocking acquire would wedge the poll
// goroutine and make the daemon unresponsive to shutdown.
const credentialLockTimeout = 30 * time.Second

// credentialLockPoll is the retry cadence while the lock is held elsewhere.
const credentialLockPoll = 100 * time.Millisecond

// ErrCredentialsLocked reports that another process held the credential lock
// for longer than credentialLockTimeout. It is transient: the caller should
// retry on a later poll rather than treat the login as broken.
var ErrCredentialsLocked = errors.New("anthropic: credentials locked by another process")

// lockAnthropicCredentials takes an exclusive advisory lock on the file that
// guards path, retrying without blocking so ctx cancellation and the timeout
// are both honoured. The returned func releases the lock.
func lockAnthropicCredentials(ctx context.Context, path string) (func(), error) {
	deadline := time.Now().Add(credentialLockTimeout)
	for {
		unlock, err := tryLockAnthropicCredentials(path)
		if err != nil {
			return nil, err
		}
		if unlock != nil {
			return unlock, nil
		}
		if time.Now().After(deadline) {
			return nil, ErrCredentialsLocked
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(credentialLockPoll):
		}
	}
}
