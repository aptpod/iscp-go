package iscp

import (
	"context"
	"sync"
	"time"

	"github.com/aptpod/iscp-go/v2/message"
)

var (
	// minResendInterval is the lower bound of the wait between the write of a
	// chunk and its first resend while connected.
	minResendInterval = 5 * time.Second
	// minMaxResendInterval is the lower bound of the upper limit of the resend
	// wait (the wait doubles on each resend up to this limit).
	minMaxResendInterval = 60 * time.Second
)

// resendScheduler tracks, per unacknowledged chunk, when it is resent next
// while the connection stays up.
//
// It exists for the multi-connection transport: when only one subconnection
// loses a chunk whose write already succeeded, the whole connection does not
// reconnect, so the resend on resume never runs for that chunk. Whether a
// chunk is still unacknowledged is decided by sentBuf (the resend set of QoS
// Reliable), not by this scheduler; an entry here is only a timer.
type resendScheduler struct {
	initialWait time.Duration
	maxWait     time.Duration

	mu      sync.Mutex
	entries map[uint32]*resendEntry
	// kickCh wakes resendLoop when an entry is added so that it can re-arm its
	// timer to the earliest due time (cap 1; a pending kick is enough).
	kickCh chan struct{}
}

type resendEntry struct {
	due  time.Time
	wait time.Duration
}

func newResendScheduler(ackInterval time.Duration) *resendScheduler {
	initialWait := max(minResendInterval, 2*ackInterval)
	return &resendScheduler{
		initialWait: initialWait,
		maxWait:     max(minMaxResendInterval, initialWait),
		entries:     make(map[uint32]*resendEntry),
		kickCh:      make(chan struct{}, 1),
	}
}

// schedule registers the first resend of seqNum after a write attempt. An
// entry that already exists keeps its schedule.
func (s *resendScheduler) schedule(seqNum uint32, now time.Time) {
	s.mu.Lock()
	if _, ok := s.entries[seqNum]; !ok {
		s.entries[seqNum] = &resendEntry{due: now.Add(s.initialWait), wait: s.initialWait}
	}
	s.mu.Unlock()
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

func (s *resendScheduler) remove(seqNum uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, seqNum)
}

// reset drops all entries. It is called when run() ends: the chunks still
// unacknowledged are resent by the resume path, whose writes register them
// again for the next session.
func (s *resendScheduler) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.entries)
}

func (s *resendScheduler) nextDue() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var next time.Time
	for _, e := range s.entries {
		if next.IsZero() || e.due.Before(next) {
			next = e.due
		}
	}
	return next, !next.IsZero()
}

// popDue returns the sequence numbers due at now and moves each of them to its
// next due time with the wait doubled (capped at maxWait).
func (s *resendScheduler) popDue(now time.Time) []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []uint32
	for seqNum, e := range s.entries {
		if e.due.After(now) {
			continue
		}
		due = append(due, seqNum)
		e.wait = min(2*e.wait, s.maxWait)
		e.due = now.Add(e.wait)
	}
	return due
}

// resendLoop resends unacknowledged chunks while connected. It is a member of
// the errgroup of run(), so it ends when the session is lost and does not
// start a resend on the old session; the resume path takes over.
func (u *Upstream) resendLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	timer.Stop()
	defer timer.Stop()
	for {
		if due, ok := u.resend.nextDue(); ok {
			timer.Reset(time.Until(due))
		}
		select {
		case <-ctx.Done():
			return
		case <-u.resend.kickCh:
		case <-timer.C:
			for _, seqNum := range u.resend.popDue(time.Now()) {
				u.resendChunk(ctx, seqNum)
			}
		}
		timer.Stop()
	}
}

// resendChunk resends the chunk of seqNum with the same data and sequence
// number through the current session. The lower layer chooses the route as
// for a normal write. The Ack is handled by processResult as for the first
// send, so a late Ack racing with the resend only causes a duplicate.
func (u *Upstream) resendChunk(ctx context.Context, seqNum uint32) {
	dpgs, ok := u.getSent(seqNum)
	if !ok {
		u.resend.remove(seqNum)
		return
	}
	// Once CloseRequest is being sent, a resend would overtake it on the wire.
	// This check is best-effort: a resend already past it cannot be stopped.
	if u.closeRequested.Load() {
		return
	}

	u.mu.Lock()
	dpg, ids := dpgs.toUpstreamDataPointGroups(u.revDataIDAliases)
	wireConn := u.wireConn
	idAlias := u.idAlias
	u.mu.Unlock()

	encoded, err := wireConn.EncodeUpstreamChunk(&message.UpstreamChunk{
		StreamIDAlias: idAlias,
		DataIDs:       ids,
		StreamChunk: &message.StreamChunk{
			SequenceNumber:  seqNum,
			DataPointGroups: dpg,
		},
	})
	if err != nil {
		u.logger.Warnf(u.ctx, "failed to encode resent upstream chunk[seq:%v]: %+v", seqNum, err)
		return
	}
	if err := wireConn.SendEncodedUpstreamChunk(ctx, encoded); err != nil {
		u.logger.Warnf(u.ctx, "failed to resend upstream chunk[seq:%v]: %+v", seqNum, err)
		return
	}
	u.logger.Debugf(u.ctx, "Resent unacknowledged upstream chunk[seq:%v] while connected. upstreamID:[%s]", seqNum, u.ID)
}
