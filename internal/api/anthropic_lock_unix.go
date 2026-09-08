//go:build !windows

package api

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLockAnthropicCredentials attempts one non-blocking exclusive flock. A nil
// unlock func with a nil error means the lock is currently held elsewhere.
func tryLockAnthropicCredentials(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, nil
		}
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
