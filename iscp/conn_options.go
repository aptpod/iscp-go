package iscp

import (
	"fmt"
	"time"

	uuid "github.com/google/uuid"

	"github.com/aptpod/iscp-go"
	"github.com/aptpod/iscp-go/encoding"
	"github.com/aptpod/iscp-go/encoding/json"
	"github.com/aptpod/iscp-go/encoding/protobuf"
	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/log"
	"github.com/aptpod/iscp-go/transport"
	"github.com/aptpod/iscp-go/transport/compress"
	"github.com/aptpod/iscp-go/transport/multi"
	"github.com/aptpod/iscp-go/transport/quic"
	"github.com/aptpod/iscp-go/transport/reconnect"
	"github.com/aptpod/iscp-go/transport/websocket"
	"github.com/aptpod/iscp-go/transport/webtransport"
	"github.com/aptpod/iscp-go/wire"
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
	MultiTransportConfig:     nil,

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

	// EXPERIMENTAL
	// MultiTransportConfigは、複数のトランスポートを使用する設定です。
	// この設定を使用すると、WebSocketConfig、QUICConfig、WebSocketConfigは無視されます。
	MultiTransportConfig *MultiTransportConfig

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

	var tr transport.Transport
	switch c.Transport {
	case TransportNameMulti:
		tr, err = c.createMultiTransport()
	default:
		tr, err = c.createSingleTransport()
	}
	if err != nil {
		return nil, err
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

func (c *ConnConfig) createMultiTransport() (transport.Transport, error) {
	if c.MultiTransportConfig == nil {
		return nil, errors.New("MultiTransportConfig is required")
	}

	if err := c.MultiTransportConfig.normalizeAndValidate(); err != nil {
		return nil, errors.Errorf("dial to [%s]: %w", c.Address, err)
	}

	trMap := multi.TransportMap{} // multi.StatusAwareTransport を値とするマップに変更
	idx := 0
	tgID := transport.TransportGroupID(uuid.NewString())
	for tID, dialer := range c.MultiTransportConfig.DialerMap {
		rtr, err := reconnect.Dial(reconnect.DialConfig{
			Dialer: dialer,
			DialConfig: transport.DialConfig{
				Address:          c.Address,
				CompressConfig:   c.CompressConfig,
				EncodingName:     transport.EncodingName(c.Encoding.toEncoding().Name()),
				TransportID:      tID,
				TransportGroupID: tgID,
			},
			MaxReconnectAttempts: c.MultiTransportConfig.MaxReconnectAttempts,
			ReconnectInterval:    c.MultiTransportConfig.ReconnectInterval,
			Logger:               c.Logger,
		})
		if err != nil {
			return nil, errors.Errorf("failed to dial to [%s]: %w", c.Address, err)
		}
		idx++
		trMap[tID] = rtr
	}

	// TransportSelectorが指定されていない場合はデフォルトでRoundRobinSelectorを使用
	transportSelector := c.MultiTransportConfig.TransportSelector
	if transportSelector == nil {
		transportIDs := make([]transport.TransportID, 0, len(trMap))
		for id := range trMap {
			transportIDs = append(transportIDs, id)
		}
		transportSelector = multi.NewRoundRobinSelector(transportIDs)
	}

	// ECFTransportUpdaterを実装している場合はloggerを設定
	if ecfUpdater, ok := transportSelector.(multi.ECFTransportUpdater); ok {
		ecfUpdater.SetLogger(c.Logger)
	}

	tr, err := multi.NewTransport(multi.TransportConfig{
		TransportMap:      trMap,
		TransportSelector: transportSelector,
		Logger:            c.Logger,
	})
	if err != nil {
		for _, t := range trMap {
			t.Close()
		}
		return nil, errors.Errorf("failed to dial to [%s]: %w", c.Address, err)
	}
	return tr, nil
}

func (c *ConnConfig) createSingleTransport() (transport.Transport, error) {
	dialer, err := c.toDialer()
	if err != nil {
		return nil, errors.Errorf("failed toDialer: %w", err)
	}

	tr, err := dialer.Dial(transport.DialConfig{
		Address:        c.Address,
		CompressConfig: c.CompressConfig,
		EncodingName:   transport.EncodingName(c.Encoding.toEncoding().Name()),
	})
	if err != nil {
		return nil, errors.Errorf("failed dialing to [%s]: %w", c.Address, err)
	}
	return tr, nil
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

// WithMultiTransportDialerMapは、複数のトランスポートのダイアラーを設定します。
func WithConnMultiTransport(c *MultiTransportConfig) ConnOption {
	return func(o *ConnConfig) {
		o.MultiTransportConfig = c
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

type MultiTransportConfig struct {
	DialerMap map[transport.TransportID]transport.Dialer

	// TransportSelector はデータサイズに基づいて最適なTransportIDを選択する実装です。
	// nil の場合、RoundRobinSelector がデフォルトで使用されます。
	TransportSelector multi.TransportSelector

	MaxReconnectAttempts int
	ReconnectInterval    time.Duration
}

func (c *MultiTransportConfig) normalizeAndValidate() error {
	// validate phase
	if len(c.DialerMap) == 0 {
		return errors.New("DialerMap is required")
	}
	if c.MaxReconnectAttempts == 0 {
		c.MaxReconnectAttempts = 30
	}
	if c.ReconnectInterval == 0 {
		c.ReconnectInterval = time.Second
	}

	return nil
}
