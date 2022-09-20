package iscp

import (
	"testing"

	"github.com/aptpod/iscp-go/transport"
	uuid "github.com/google/uuid"
)

var (
	ToUpstreamDataPointGroups    = (*DataPointGroups).toUpstreamDataPointGroups
	NewInmemSentStorage          = newInmemSentStorage
	NewInmemSentStorageNoPayload = newInmemSentStorageNoPayload
)

type (
	SequenceNumberGenerator         = sequenceNumberGenerator
	ConnState                       = connState
	ConnStatus                      = connStatus
	FlushPolicyNone                 = flushPolicyNone
	FlushPolicyIntervalOnly         = flushPolicyIntervalOnly
	FlushPolicyIntervalOrBufferSize = flushPolicyIntervalOrBufferSize
	FlushPolicyBufferSizeOnly       = flushPolicyBufferSizeOnly
	FlushPolicyImmediately          = flushPolicyImmediately
	SentStorage                     = sentStorage
)

func (u *Upstream) IsReceivedLastSentAck() bool {
	return u.sequence.CurrentValue() == u.upState.maxSequenceNumberInReceivedUpstreamChunkResults
}

var (
	GetUpstreamState       = (*Upstream).State
	GetDownstreamState     = (*Downstream).State
	ConnStatusConnected    = connStatusConnected
	ConnStatusReconnecting = connStatusReconnecting
)

func (u *Upstream) ID() uuid.UUID {
	if u == nil {
		return uuid.Nil
	}
	return u.upState.ID
}

func (d *Downstream) ID() uuid.UUID {
	if d == nil {
		return uuid.Nil
	}
	return d.downState.ID
}

func SetRandomString(t *testing.T, fix string) {
	org := randomString
	randomString = func() string { return fix }
	t.Cleanup(func() {
		randomString = org
	})
}

func RegisterDialer(tr Transport, f func() transport.Dialer) {
	customDialFuncs[tr] = f
}
