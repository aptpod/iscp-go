package iscp

import (
	"context"

	"github.com/aptpod/iscp-go/errors"
	"github.com/google/uuid"

	"github.com/aptpod/iscp-go/message"
)

var randomString = func() string {
	return uuid.NewString()
}

// SendCallは、E2Eコールを送信します。
func (c *Conn) SendCall(ctx context.Context, request *UpstreamCall) (callID string, err error) {
	if c.isClosed() {
		return "", errors.ErrConnectionClosed
	}
	callID = randomString()
	res, err := c.call(ctx, &message.UpstreamCall{
		CallID:            callID,
		RequestCallID:     "",
		DestinationNodeID: request.DestinationNodeID,
		Name:              request.Name,
		Type:              request.Type,
		Payload:           request.Payload,
		ExtensionFields:   &message.UpstreamCallExtensionFields{},
	})
	if err != nil {
		return "", err
	}
	if res.ResultCode == message.ResultCodeSucceeded {
		return callID, nil
	}
	return "", &errors.FailedMessageError{
		ResultCode:      res.ResultCode,
		ResultString:    res.ResultString,
		ReceivedMessage: res,
	}
}

// ReceiveCallは、E2Eコールを受信します。
func (c *Conn) ReceiveCall(ctx context.Context) (*DownstreamCall, error) {
	ctx, cancel := c.state.WithCloseStatus(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		if c.state.Is(connStatusClosed) {
			return nil, errors.ErrConnectionClosed
		}
		return nil, ctx.Err()
	case call := <-c.downstreamCallCh:
		return &DownstreamCall{
			CallID:       call.CallID,
			SourceNodeID: call.SourceNodeID,
			Name:         call.Name,
			Type:         call.Type,
			Payload:      call.Payload,
		}, nil
	}
}

// ReceiveReplyCallは、E2Eリプライコールを受信します。
func (c *Conn) ReceiveReplyCall(ctx context.Context) (*DownstreamReplyCall, error) {
	ctx, cancel := c.state.WithCloseStatus(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		if c.state.Is(connStatusClosed) {
			return nil, errors.ErrConnectionClosed
		}
		return nil, ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	case call := <-c.replyCallCh:
		return &DownstreamReplyCall{
			CallID:        call.CallID,
			RequestCallID: call.RequestCallID,
			SourceNodeID:  call.SourceNodeID,
			Name:          call.Name,
			Type:          call.Type,
			Payload:       call.Payload,
		}, nil
	}
}

// SendReplyCallは、リプライコールを送信します。
func (c *Conn) SendReplyCall(ctx context.Context, request *UpstreamReplyCall) (callID string, err error) {
	if c.isClosed() {
		return "", errors.ErrConnectionClosed
	}
	callID = randomString()
	res, err := c.call(ctx, &message.UpstreamCall{
		CallID:            callID,
		RequestCallID:     request.RequestCallID,
		DestinationNodeID: request.DestinationNodeID,
		Name:              request.Name,
		Type:              request.Type,
		Payload:           request.Payload,
		ExtensionFields:   &message.UpstreamCallExtensionFields{},
	})
	if err != nil {
		return "", err
	}
	if res.ResultCode == message.ResultCodeSucceeded {
		return callID, nil
	}
	return "", errors.FailedMessageError{
		ResultCode:      res.ResultCode,
		ResultString:    res.ResultString,
		ReceivedMessage: res,
	}
}

// SendCallAndWaitReplayCallは、コールし、それに対応するリプライコールを受信します。
//
// このメソッドはリプライコールを受信できるまで処理をブロックします。
func (c *Conn) SendCallAndWaitReplayCall(ctx context.Context, request *UpstreamCall) (reply *DownstreamReplyCall, err error) {
	callID := randomString()
	ch, err := c.subscribeReply(callID)
	if err != nil {
		return nil, err
	}

	ack, err := c.call(ctx, &message.UpstreamCall{
		CallID:            callID,
		RequestCallID:     "",
		DestinationNodeID: request.DestinationNodeID,
		Name:              request.Name,
		Type:              request.Type,
		Payload:           request.Payload,
		ExtensionFields:   &message.UpstreamCallExtensionFields{},
	})
	if err != nil {
		return nil, err
	}

	if ack.ResultCode != message.ResultCodeSucceeded {
		return nil, errors.New(ack.ResultString)
	}

	return c.receiveReplyCall(ctx, ch)
}

func (c *Conn) subscribeReply(requestID string) (<-chan *message.DownstreamCall, error) {
	c.replyCallsChsMu.RLock()
	_, ok := c.replyCallChs[requestID]
	c.replyCallsChsMu.RUnlock()
	if ok {
		return nil, errors.New("already exist reply for call id")
	}

	ch := make(chan *message.DownstreamCall, 1)
	c.replyCallsChsMu.Lock()
	c.replyCallChs[requestID] = ch
	c.replyCallsChsMu.Unlock()
	return ch, nil
}

func (c *Conn) receiveReplyCall(ctx context.Context, ch <-chan *message.DownstreamCall) (*DownstreamReplyCall, error) {
	ctx, cancel := c.state.WithCloseStatus(ctx)
	defer cancel()
	select {
	case <-ctx.Done():
		if c.state.Is(connStatusClosed) {
			return nil, errors.ErrConnectionClosed
		}
		return nil, ctx.Err()
	case call := <-ch:
		return &DownstreamReplyCall{
			CallID:        call.CallID,
			RequestCallID: call.RequestCallID,
			SourceNodeID:  call.SourceNodeID,
			Name:          call.Name,
			Type:          call.Type,
			Payload:       call.Payload,
		}, nil
	}
}

func (c *Conn) call(ctx context.Context, msg *message.UpstreamCall) (*message.UpstreamCallAck, error) {
	ctx, cancel := c.state.WithCloseStatus(ctx)
	defer cancel()
	c.upstreamCallAckMu.RLock()
	_, ok := c.upstreamCallAckCh[msg.CallID]
	c.upstreamCallAckMu.RUnlock()
	if ok {
		return nil, errors.New("already exist call id")
	}
	ch := make(chan *message.UpstreamCallAck, 1)
	c.upstreamCallAckMu.Lock()
	c.upstreamCallAckCh[msg.CallID] = ch
	c.upstreamCallAckMu.Unlock()

	err := c.send(ctx, func(ctx context.Context) error {
		c.wireConnMu.Lock()
		defer c.wireConnMu.Unlock()
		return c.wireConn.SendUpstreamCall(ctx, msg)
	})
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		if c.state.Is(connStatusClosed) {
			return nil, errors.ErrConnectionClosed
		}
		return nil, ctx.Err()
	case ack := <-ch:
		return ack, nil
	}
}
