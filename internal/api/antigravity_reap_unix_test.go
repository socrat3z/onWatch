//go:build !windows

package api

import (
	"context"
	"testing"

	pty "github.com/aymanbagabas/go-pty"
)

func TestAgySessionCleanupReapsProcess(t *testing.T) {
	for _, path := range []string{"eviction", "stop", "readiness failure"} {
		t.Run(path, func(t *testing.T) {
			restore := agyResidentSnapshotForTest()
			defer restore()
			runner := NewAntigravityCLIRunner(nil)
			defer runner.Stop()
			p, err := pty.New()
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			cmd := p.Command("/bin/sh", "-c", "exec sleep 60")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if cmd.ProcessState == nil {
					killAgyTree(cmd.Process)
					_ = cmd.Wait()
				}
			})
			sess := &agySession{pty: p, cmd: cmd}
			switch path {
			case "eviction":
				runner.sess = sess
				runner.evict(1)
			case "stop":
				runner.sess = sess
				runner.Stop()
			case "readiness failure":
				err := runner.launchReservedSession(context.Background(), func() (*agySession, error) {
					return sess, nil
				}, func(context.Context, *agySession) (*AntigravityConnection, error) {
					return nil, ErrAgyNotReady
				})
				if err != ErrAgyNotReady {
					t.Fatalf("startup error = %v", err)
				}
			}
			if cmd.ProcessState == nil {
				t.Fatal("managed process was killed without being waited on")
			}
		})
	}
}
