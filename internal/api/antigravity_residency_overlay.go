package api

import "context"

// launchReservedSession reserves a resident slot before starting a process and
// releases it again if startup fails, so a slow or failed launch can never
// overlap the resident it replaced. Caller must hold r.mu.
func (r *AntigravityCLIRunner) launchReservedSession(ctx context.Context, start func() (*agySession, error), ready func(context.Context, *agySession) (*AntigravityConnection, error)) error {
	agyAdmitResident(r)
	sess, err := start()
	if err != nil {
		agyForgetResident(r)
		return err
	}
	conn, err := ready(ctx, sess)
	if err != nil {
		r.killSession(sess)
		agyForgetResident(r)
		return err
	}
	sess.conn = conn
	r.sess = sess
	r.logger.Info("agy session ready", "pid", sess.cmd.Process.Pid, "port", conn.Port)
	return nil
}
