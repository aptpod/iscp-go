package iscp

import (
	"context"
	"fmt"

	"github.com/aptpod/iscp-go/v2/errors"
	"github.com/aptpod/iscp-go/v2/internal/retry"
)

// connLifecycle manages the connection lifecycle (disconnect detection -> reconnect -> resume of all streams).
// It is started as a goroutine from ConnectWithConfig.
//
// Resume sequence:
//  1. waitForDisconnect() — wait for wireConn to disconnect or the state to change
//  2. reconnect()        — reconnect the transport and update wireConn
//  3. resumeAllStreams()  — call resume() on all upstreams/downstreams
//  4. state -> connStatusConnected (allow stream goroutines to resume)
//  5. Notify ReconnectedEventHandler
//  6. Return to the top of the loop
type connLifecycle struct {
	conn *Conn
}

func (cl *connLifecycle) run(ctx context.Context) {
	for {
		ctx, cancel := cl.conn.state.WithCloseStatus(ctx)
		go func() {
			cl.conn.eventDispatcher.dispatchLoop(ctx)
		}()

		if err := cl.waitForDisconnect(ctx); err != nil {
			if err := cl.reconnect(ctx); err != nil {
				if errors.Is(err, errors.ErrConnectionClosed) {
					cl.conn.logger.Warnf(ctx, "failed to reconnect: %+v", err)
					cancel()
					return
				}
				cl.conn.logger.Errorf(ctx, "failed to reconnect: %+v", err)
				cancel()
				return
			}
			cl.resumeAllStreams(ctx)
			// Transition to connected after resumeAllStreams completes.
			// Stream goroutines are blocked on WaitUntil(connStatusConnected), and
			// resume u.run()/down.run() after this transition.
			if !cl.conn.state.CompareAndSwap(connStatusReconnecting, connStatusConnected) {
				// Close() was called during reconnect or resumeAllStreams
				cancel()
				return
			}
			cl.conn.Config.ReconnectedEventHandler.OnReconnected(&ReconnectedEvent{
				Config: cl.conn.Config,
			})
			cancel()
			continue
		}
		cancel()
		return
	}
}

// waitForDisconnect waits for wireConn to disconnect or for an explicit close.
// It returns an error on disconnect detection to start the reconnect sequence.
func (cl *connLifecycle) waitForDisconnect(ctx context.Context) error {
	defer cl.conn.Config.DisconnectedEventHandler.OnDisconnected(&DisconnectedEvent{
		Config: cl.conn.Config,
	})
	for {
		changed := cl.conn.state.Changed()
		select {
		case <-ctx.Done():
			return nil
		case <-cl.conn.wireConn.Closed():
			if cl.conn.state.Is(connStatusClosed) {
				return nil
			}
			return fmt.Errorf("unexpected disconnect: %w", errors.New("unexpected disconnected"))
		case <-changed:
			if cl.conn.state.Is(connStatusClosed) {
				return nil
			}
			if cl.conn.state.Is(connStatusReconnecting) {
				return fmt.Errorf("unexpected disconnect: %w", errors.New("unexpected transport closed"))
			}
		}
	}
}

// reconnect reconnects the transport and updates wireConn.
// It does not transition the state to connStatusConnected. The transition is
// done in run() after resumeAllStreams() completes, to prevent a race with
// stream goroutines.
func (cl *connLifecycle) reconnect(ctx context.Context) error {
	// wireConnMu is held only for the short sections of "reading the old session's
	// pointer" and "assigning the new session", and closing the old session and
	// dialing are done outside the lock. Doing either inside the lock would drag
	// even Close() into waiting for the lock when they block (this used to be
	// mitigated with TryLock + timeout). Whether close is bounded depends on the
	// underlying transport: reconnect.Transport interrupts an in-progress dial
	// with its own ctx, so it is bounded, but when a legacy Dialer is plugged in
	// it may wait up to the dialer's internal timeout (see the doc of
	// transport/reconnect). This path, entered on a server disconnect, is called
	// exactly while each sub-connection is running its dial loop.
	//
	// Basis for mutual exclusion: CompareAndSwapNot only provides atomicity of the
	// state transition, not mutual exclusion of this function. This section is
	// safe because reconnect() is called only from the lifecycle goroutine (the
	// loop in connLifecycle.run), so there is no concurrent execution. Writes to
	// wireConn are also limited to those this goroutine makes under wireConnMu.
	cl.conn.wireConnMu.Lock()
	if !cl.conn.state.CompareAndSwapNot(connStatusClosed, connStatusReconnecting) {
		cl.conn.wireConnMu.Unlock()
		return errors.ErrConnectionClosed
	}
	old := cl.conn.wireConn
	cl.conn.wireConnMu.Unlock()
	// close() may also close the same old session, but protocolSession.Close is
	// safe to call concurrently and multiple times, thanks to ctx cancel
	// (idempotent) and exclusion on the transport side.
	// Note that resumeAllStreams / waitForDisconnect, which read wireConn without
	// a lock, are correct because they run on the same lifecycle goroutine as the
	// writes. Do not move these reads to another goroutine.
	old.Close()

	var res *protocolSession
	var resErr error
	// ctx is canceled when the state becomes connStatusClosed
	// (WithCloseStatus in run()). After Close, it is aborted immediately even
	// during the sleep between retries, and no further dial attempts are made.
	// Because ctx is passed to dialWire, an in-progress dial and handshake are
	// also interrupted (with a legacy Dialer only, it may take up to the
	// dialer's internal timeout). All of these are outside the lock, so they do
	// not block Close.
	retry.DoWithContext(ctx, func() (end bool) {
		cl.conn.logger.Infof(ctx, "Try reconnecting...")
		res, resErr = cl.conn.Config.dialWire(ctx)
		if resErr != nil {
			return cl.conn.state.Is(connStatusClosed)
		}
		cl.conn.logger.Infof(ctx, "Reconnected")
		return true
	})
	if res == nil {
		// Aborted without dial ever succeeding. resErr remains nil only when f was
		// never called because of ctx cancellation.
		if resErr == nil || cl.conn.state.Is(connStatusClosed) {
			return errors.ErrConnectionClosed
		}
		return resErr
	}
	// If Close() was called during dial, detect it before assigning to wireConn
	// and close the new session (if not detected, nobody closes the new session
	// and it leaks. This was a regression introduced when this was changed to
	// use TryLock). The check and the assignment are done under the same
	// wireConnMu as close(), so nothing is missed:
	//
	//   - If close()'s state.Swap(closed) completed before this check, it is
	//     always detected here and res.Close() is called
	//   - A close() that did Swap after the check always reads and closes the
	//     assigned new wireConn after waiting for the lock (that is, after the
	//     assignment and Unlock)
	//
	// Previously, close() had a path that gave up with TryLock, and a leak window
	// of "a close() that gave up racing with this check-to-assignment" could in
	// theory remain. Since dial was moved outside the lock and close() went back
	// to a plain Lock(), that window structurally no longer exists.
	cl.conn.wireConnMu.Lock()
	defer cl.conn.wireConnMu.Unlock()
	if cl.conn.state.Is(connStatusClosed) {
		res.Close()
		return errors.ErrConnectionClosed
	}
	cl.conn.wireConn = res
	cl.conn.setE2ECallbacks(res)
	// Start after setE2ECallbacks completes (for the same reason as in
	// ConnectWithConfig; see the comment on newProtocolSession).
	go res.runWire()
	return nil
}

// resumeAllStreams calls resume() on all upstreams/downstreams.
// An individual resume failure is logged and execution continues (the same behavior as the current per-stream goroutine loops).
func (cl *connLifecycle) resumeAllStreams(ctx context.Context) {
	// Narrow the lock scope to taking the snapshot only.
	// resume() involves network I/O, so calling it while holding the lock
	// would block Close() -> saveAndClearAllUpstreams().
	cl.conn.upstreamMu.Lock()
	upstreams := make([]*Upstream, 0, len(cl.conn.upstreams))
	for u := range cl.conn.upstreams {
		upstreams = append(upstreams, u)
	}
	cl.conn.upstreamMu.Unlock()

	for _, u := range upstreams {
		if err := u.resume(cl.conn.wireConn); err != nil {
			cl.conn.logger.Errorf(ctx, "failed to resume upstream [%s]: %+v", u.ID, err)
		} else {
			cl.conn.logger.Infof(ctx, "Succeeded in resuming upstream %v", u.ID.String())
		}
	}

	cl.conn.downstreamMu.Lock()
	downstreams := make([]*Downstream, 0, len(cl.conn.downstreams))
	for d := range cl.conn.downstreams {
		downstreams = append(downstreams, d)
	}
	cl.conn.downstreamMu.Unlock()

	for _, d := range downstreams {
		if err := d.resume(cl.conn); err != nil {
			cl.conn.logger.Errorf(ctx, "failed to resume downstream [%s]: %+v", d.ID, err)
		} else {
			cl.conn.logger.Infof(ctx, "Succeeded in resuming downstream [%v]", d.ID)
		}
	}
}
