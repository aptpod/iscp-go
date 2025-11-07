package iscp_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/aptpod/iscp-go/iscp"
	. "github.com/aptpod/iscp-go/iscp"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/message"
	"github.com/aptpod/iscp-go/transport"
)

func TestE2E_Request(t *testing.T) {
	upNodeID := "11111111-1111-1111-1111-111111111111"
	echoNode := "22222222-2222-2222-2222-222222222222"
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
		{
			name: "success ws reliable",
			qos:  message.QoSReliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				assert.Equal(t, &message.UpstreamCall{
					CallID:            "first",
					RequestCallID:     "",
					DestinationNodeID: echoNode,
					Name:              "name",
					Type:              "type",
					Payload:           []byte{1, 2, 3, 4, 5},
					ExtensionFields:   &message.UpstreamCallExtensionFields{},
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
				mustWrite(t, d.srv, &message.UpstreamCallAck{
					CallID:          "first",
					ResultCode:      message.ResultCodeSucceeded,
					ResultString:    "OK",
					ExtensionFields: &message.UpstreamCallAckExtensionFields{},
				})
				mustWrite(t, d.srv, &message.DownstreamCall{
					CallID:          "echo_back",
					RequestCallID:   "first",
					SourceNodeID:    echoNode,
					Name:            "name",
					Type:            "type",
					Payload:         []byte{1, 2, 3, 4, 5},
					ExtensionFields: &message.DownstreamCallExtensionFields{},
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			upConn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID(upNodeID),
			)
			require.NoError(t, err)
			defer upConn.Close(ctx)

			SetRandomString(t, "first")

			stub := &UpstreamCall{
				DestinationNodeID: echoNode,
				Name:              "name",
				Type:              "type",
				Payload:           []byte{1, 2, 3, 4, 5},
			}

			got, err := upConn.SendCallAndWaitReplayCall(ctx, stub)
			require.NoError(t, err)

			assert.Equal(t, &DownstreamReplyCall{
				CallID:        "echo_back",
				RequestCallID: "first",
				SourceNodeID:  echoNode,
				Name:          stub.Name,
				Type:          stub.Type,
				Payload:       stub.Payload,
			}, got)
		})
	}
}

func TestE2E_ReceiveReplyCall(t *testing.T) {
	upNodeID := "11111111-1111-1111-1111-111111111111"
	echoNode := "22222222-2222-2222-2222-222222222222"
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				mustWrite(t, d.srv, &message.DownstreamCall{
					CallID:          "echo_back",
					RequestCallID:   "first",
					SourceNodeID:    echoNode,
					Name:            "name",
					Type:            "type",
					Payload:         []byte{1, 2, 3, 4, 5},
					ExtensionFields: &message.DownstreamCallExtensionFields{},
				})
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			upConn, err := Connect("dummy", TransportTest, iscp.WithConnNodeID(upNodeID))
			require.NoError(t, err)
			defer upConn.Close(ctx)

			SetRandomString(t, "first")

			call, err := upConn.ReceiveReplyCall(ctx)
			require.NoError(t, err)

			want := &DownstreamReplyCall{
				CallID:        "echo_back",
				RequestCallID: "first",
				SourceNodeID:  echoNode,
				Name:          "name",
				Type:          "type",
				Payload:       []byte{1, 2, 3, 4, 5},
			}
			assert.Equal(t, want, call)
		})
	}
}

func TestE2E_ConnClosed(t *testing.T) {
	tests := []struct {
		name string
		qos  message.QoS
	}{
		{
			name: "success unreliable",
			qos:  message.QoSUnreliable,
		},
		{
			name: "success reliable",
			qos:  message.QoSReliable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer goleak.VerifyNone(t)
			d := newDialer(transport.NegotiationParams{})
			RegisterDialer(TransportTest, func() transport.Dialer { return d })
			done := make(chan struct{}, 0)
			defer func() {
				<-done
			}()
			go func() {
				defer close(done)
				mockConnectRequest(t, d.srv)
				assert.Equal(t, &message.Disconnect{
					ResultCode:   message.ResultCodeSucceeded,
					ResultString: "NormalClosure",
				}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
			}()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			conn, err := Connect("dummy", TransportTest,
				iscp.WithConnNodeID("11111111-1111-1111-1111-111111111111"),
			)
			require.NoError(t, err)

			err = conn.Close(ctx)
			require.NoError(t, err)

			stub := &UpstreamCall{
				Name:    "n",
				Type:    "t",
				Payload: []byte{1},
			}

			_, err = conn.SendCall(ctx, stub)
			require.Error(t, err)

			_, err = conn.ReceiveCall(ctx)
			require.Error(t, err)

			_, err = conn.SendCallAndWaitReplayCall(ctx, stub)
			require.Error(t, err)

			assert.NoError(t, conn.Close(ctx))
		})
	}
}

func TestE2E_Reconnect_Publish(t *testing.T) {
	defer goleak.VerifyNone(t)
	nodeID := "11111111-1111-1111-1111-111111111111"
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest,
		func() transport.Dialer {
			callCount++
			return ds[callCount-1]
		},
	)
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		d := ds[0]
		mockConnectRequest(t, d.srv)
		assert.Equal(t, &message.UpstreamCall{
			CallID:            "call-id",
			RequestCallID:     "",
			DestinationNodeID: nodeID,
			Name:              "name",
			Type:              "string",
			Payload:           []byte("first"),
			ExtensionFields:   &message.UpstreamCallExtensionFields{},
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
		mustWrite(t, d.srv, &message.UpstreamCallAck{
			CallID:          "call-id",
			ResultCode:      message.ResultCodeSucceeded,
			ResultString:    "OK",
			ExtensionFields: &message.UpstreamCallAckExtensionFields{},
		})
	}()

	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		assert.Equal(t, &message.UpstreamCall{
			CallID:            "call-id",
			RequestCallID:     "",
			DestinationNodeID: nodeID,
			Name:              "name",
			Type:              "string",
			Payload:           []byte("second"),
			ExtensionFields:   &message.UpstreamCallExtensionFields{},
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
		mustWrite(t, d.srv, &message.UpstreamCallAck{
			CallID:          "call-id",
			ResultCode:      message.ResultCodeSucceeded,
			ResultString:    "OK",
			ExtensionFields: &message.UpstreamCallAckExtensionFields{},
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnPingInterval(time.Second),
		iscp.WithConnLogger(log.NewStd()),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)
	SetRandomString(t, "call-id")
	args := &iscp.UpstreamCall{
		DestinationNodeID: nodeID,
		Name:              "name",
		Type:              "string",
		Payload:           []byte("first"),
	}

	callID, err := conn.SendCall(ctx, args)
	require.NoError(t, err)
	assert.Equal(t, "call-id", callID)

	ds[0].srv.Close()
	args.Payload = []byte("second")
	_, err = conn.SendCall(ctx, args)
	require.NoError(t, err)
}

func TestE2E_Reconnect_Receive(t *testing.T) {
	defer goleak.VerifyNone(t)
	nodeID := "11111111-1111-1111-1111-111111111111"
	ds := []*dialer{newDialer(transport.NegotiationParams{}), newDialer(transport.NegotiationParams{})}
	var callCount int
	RegisterDialer(TransportTest,
		func() transport.Dialer {
			callCount++
			return ds[callCount-1]
		},
	)
	done := make(chan struct{}, 0)
	defer func() {
		<-done
	}()
	go func() {
		d := ds[0]
		mockConnectRequest(t, d.srv)
	}()

	go func() {
		defer close(done)
		d := ds[1]
		mockConnectRequest(t, d.srv)
		mustWrite(t, d.srv, &message.DownstreamCall{
			CallID:          "call-id",
			RequestCallID:   "",
			SourceNodeID:    nodeID,
			Name:            "name",
			Type:            "string",
			Payload:         []byte("hello-world"),
			ExtensionFields: &message.DownstreamCallExtensionFields{},
		})
		assert.Equal(t, &message.Disconnect{
			ResultCode:   message.ResultCodeSucceeded,
			ResultString: "NormalClosure",
		}, mustRead(t, d.srv, &message.Ping{}, &message.Pong{}))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	conn, err := Connect("dummy", TransportTest,
		iscp.WithConnNodeID(nodeID),
		iscp.WithConnPingInterval(time.Second),
		iscp.WithConnLogger(log.NewStd()),
	)
	require.NoError(t, err)
	defer conn.Close(ctx)

	gotCh := make(chan *DownstreamCall, 1)
	errCh := make(chan error, 1)
	go func() {
		got, err := conn.ReceiveCall(ctx)
		errCh <- err
		gotCh <- got
	}()
	ds[0].Close()
	defer conn.Close(ctx)
	want := &DownstreamCall{
		CallID:       "call-id",
		SourceNodeID: nodeID,
		Name:         "name",
		Type:         "string",
		Payload:      []byte("hello-world"),
	}
	assert.Nil(t, err)
	assert.Equal(t, want, <-gotCh)
}
