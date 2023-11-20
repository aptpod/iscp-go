package iscp

import (
	"fmt"
	"time"

	"github.com/aptpod/iscp-go"
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/aptpod/iscp-go/transport/quic"
	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/webtransport"
	"github.com/aptpod/iscp-go/wire"
	uuid "github.com/google/uuid"
)

var defaultClientConfig = ConnConfig{
	Address:                  "",
	Transport:                "",
	WebSocketConfig:          nil,
	QUICConfig:               nil,
	WebTransportConfig:       nil,
	Logger:                   log.NewNop(),
	Encoding:                 EncodingNameProtobuf,
	CompressConfig:           compress.Config{},
	NodeID:                   "",
	PingInterval:             defaultPingInterval,
	PingTimeout:              defaultPingTimeout,
	ProjectUUID:              uuid.Nil,
	TokenSource:              NewStaticTokenSource(""),
	ReconnectedEventHandler:  nopReconnectedEventHandler{},
	DisconnectedEventHandler: nopDisconnectedEventHandler{},

	// 状態を持つものはnilをデフォルトとする。
	sentStorage:          nil,
	upstreamRepository:   nil,
	downstreamRepository: nil,
}

// ConnConfigは、コネクションの設定です。
type ConnConfig struct {
	// アドレス
	//
	// ホスト:ポート（e.g. 127.0.0.1:8080）という形式で指定します。
	Address string

	// iSCPメッセージのトランスポート
	Transport TransportName

	// WebSocketの設定
	//
	// WebSocketのときにのみ有効な設定です。
	WebSocketConfig *websocket.DialerConfig

	// QUICの設定
	//
	// QUICのときにのみ有効な設定です。
	QUICConfig *quic.DialerConfig

	// WebTransportの設定
	//
	// WebTransportのときにのみ有効な設定です。
	WebTransportConfig *webtransport.DialerConfig

	// ロガー
	Logger log.Logger

	// iSCPメッセージのエンコーディング
	Encoding EncodingName

	// トランスポートの圧縮設定
	CompressConfig compress.Config

	// ノードID
	NodeID string

	// Pingを送信する間隔
	PingInterval time.Duration

	// Pingタイムアウト
	//
	// タイムアウトするとコネクションは強制的に閉じられます。
	PingTimeout time.Duration

	// ProjectのUUID
	ProjectUUID uuid.UUID

	// トークンソース
	TokenSource TokenSource

	// 再接続が完了した時のイベントハンドラ
	ReconnectedEventHandler ReconnectedEventHandler

	// コネクションが切断されたときのイベントハンドラ
	DisconnectedEventHandler DisconnectedEventHandler

	// 送信済みのデータポイントを一時的に保存するためのストレージ
	//
	// ストレージに保存されたデータポイントはUpstreamChunkAckを受信した時点で削除します。
	sentStorage sentStorage

	// アップストリーム情報を保存するリポジトリ
	upstreamRepository upstreamRepository

	// ダウンストリームの情報を保存するリポジトリ
	downstreamRepository downstreamRepository
}

// for testing
var customDialFuncs = map[TransportName]func() transport.Dialer{}

func (c *ConnConfig) toDialer() (transport.Dialer, error) {
	switch c.Transport {
	case TransportNameWebSocket:
		if c.WebSocketConfig == nil {
			return websocket.NewDefaultDialer(), nil
		}
		return websocket.NewDialer(*c.WebSocketConfig), nil
	case TransportNameQUIC:
		if c.QUICConfig == nil {
			return quic.NewDefaultDialer(), nil
		}
		return quic.NewDialer(*c.QUICConfig), nil
	case TransportNameWebTransport:
		if c.WebTransportConfig == nil {
			return webtransport.NewDefaultDialer(), nil
		}
		return webtransport.NewDialer(*c.WebTransportConfig), nil
	default:
		dialer, ok := customDialFuncs[c.Transport]
		if !ok {
			return nil, errors.New("Unsupported transport")
		}
		return dialer(), nil
	}
}

func (c *ConnConfig) connectWire() (*wire.ClientConn, error) {
	token, err := c.TokenSource.Token()
	if err != nil {
		return nil, errors.Errorf("failed to fetch token: %w", err)
	}

	dialer, err := c.toDialer()
	if err != nil {
		return nil, errors.Errorf("failed toDialer: %w", err)
	}

	tr, err := dialer.Dial(transport.DialConfig{
		Address:        c.Address,
		EncodingName:   transport.EncodingName(c.Encoding.toEncoding().Name()),
		CompressConfig: c.CompressConfig,
	})
	if err != nil {
		return nil, errors.Errorf("failed dialing to [%s]: %w", c.Address, err)
	}

	enc := resolveEncoding(tr.NegotiationParams().Encoding)

	wtr := encoding.NewTransport(&encoding.TransportConfig{
		Transport:      tr,
		Encoding:       enc,
		MaxMessageSize: 0,
	})
	conn, err := wire.Connect(&wire.ClientConnConfig{
		Transport:           wtr,
		UnreliableTransport: unreliableOrNil(tr),
		AccessToken:         string(token),
		IntdashExtensionFields: &wire.IntdashExtensionFields{
			ProjectUUID: c.ProjectUUID,
		},
		Logger:          c.Logger,
		ProtocolVersion: iscp.ProtocolVersion,
		PingInterval:    c.PingInterval,
		PingTimeout:     c.PingTimeout,
		NodeID:          c.NodeID,
	})
	if err != nil {
		tr.Close()
		return nil, fmt.Errorf("failed wire.Connect: %w", err)
	}
	return conn, nil
}

// DefaultConnConfigは、デフォルトのConnConfigを取得します。
func DefaultConnConfig() *ConnConfig {
	c := defaultClientConfig
	return &c
}

// ConnOptionは、Connのオプションです。
type ConnOption func(*ConnConfig)

// WithConnEncodingは、エンコーディングを設定します。
func WithConnEncoding(e EncodingName) ConnOption {
	return func(o *ConnConfig) {
		o.Encoding = e
	}
}

// WithConnCompressは、トランスポートの圧縮設定を設定します。
func WithConnCompress(c compress.Config) ConnOption {
	return func(o *ConnConfig) {
		o.CompressConfig = c
	}
}

// WithConnLoggerは、ロガーを設定します。
func WithConnLogger(l log.Logger) ConnOption {
	return func(o *ConnConfig) {
		o.Logger = l
	}
}

// WithWebSocketは、トランスポート層のWebSocketの設定をします。
func WithConnWebSocket(c websocket.DialerConfig) ConnOption {
	return func(o *ConnConfig) {
		o.WebSocketConfig = &c
	}
}

// WithConnQUICは、トランスポート層のQUICの設定をします。
func WithConnQUIC(c quic.DialerConfig) ConnOption {
	return func(o *ConnConfig) {
		o.QUICConfig = &c
	}
}

// WithWebTransportは、トランスポート層のWebTransportの設定をします。
func WithConnWebTransport(c webtransport.DialerConfig) ConnOption {
	return func(o *ConnConfig) {
		o.WebTransportConfig = &c
	}
}

// WithConnTokenSourceは、トークンソースの設定をします。
func WithConnTokenSource(c TokenSource) ConnOption {
	return func(o *ConnConfig) {
		o.TokenSource = c
	}
}

// WithConnPingIntervalは、Pingを送信する間隔の設定をします。
func WithConnPingInterval(c time.Duration) ConnOption {
	return func(o *ConnConfig) {
		o.PingInterval = c
	}
}

// WithConnPingTimeoutは、Pingタイムアウトの設定をします。
//
// タイムアウトするとコネクションは強制的に閉じられ、再接続処理へ移行します。
func WithConnPingTimeout(c time.Duration) ConnOption {
	return func(o *ConnConfig) {
		o.PingTimeout = c
	}
}

// WithConnProjectUUIDは、ProjectのUUIDの設定をします。
func WithConnProjectUUID(c uuid.UUID) ConnOption {
	return func(o *ConnConfig) {
		o.ProjectUUID = c
	}
}

// WithConnNodeIDは、ノードIDの設定をします。
func WithConnNodeID(c string) ConnOption {
	return func(o *ConnConfig) {
		o.NodeID = c
	}
}

// WithConnReconnectedEventHandlerは、再接続のイベントハンドラの設定をします。
func WithConnReconnectedEventHandler(h ReconnectedEventHandler) ConnOption {
	return func(o *ConnConfig) {
		o.ReconnectedEventHandler = h
	}
}

// WithConnDisconnectedEventHandlerは、コネクションが切断されたときのイベントハンドラの設定をします。
func WithConnDisconnectedEventHandler(h DisconnectedEventHandler) ConnOption {
	return func(o *ConnConfig) {
		o.DisconnectedEventHandler = h
	}
}

func resolveEncoding(enc transport.EncodingName) encoding.Encoding {
	switch enc {
	case transport.EncodingNameProtobuf:
		return protobuf.NewEncoding()
	case transport.EncodingNameJSON:
		return json.NewEncoding()
	default:
		return protobuf.NewEncoding()
	}
}

func unreliableOrNil(tr transport.Transport) wire.EncodingTransport {
	ut, ok := tr.AsUnreliable()
	if !ok {
		return nil
	}

	wtr := encoding.NewTransport(&encoding.TransportConfig{
		Transport:      ut,
		Encoding:       resolveEncoding(tr.NegotiationParams().Encoding),
		MaxMessageSize: 0,
	})
	return wtr
}
