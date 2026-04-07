package transport

import (
	"fmt"

	"github.com/aptpod/iscp-go/v2/transport/compress"
	"github.com/aptpod/iscp-go/v2/transport/metrics"
	"github.com/aptpod/iscp-go/v2/transport/protocol"
)

// v4CompressWrite は v4 プロトコルのメッセージタイプに基づいて選択的に圧縮し、writer に書き込みます。
// iSCP メッセージ（0x00）の body のみ圧縮し、制御メッセージはそのまま通します。
func v4CompressWrite(writer Writer, compressor compress.Compressor, bs []byte) error {
	if len(bs) == 0 || bs[0] != byte(protocol.MessageTypeISCP) {
		return writer.Write(bs)
	}
	compressed, err := compressor.Encode(bs[1:])
	if err != nil {
		return fmt.Errorf("compress iSCP message: %w", err)
	}
	return writer.Write(append([]byte{bs[0]}, compressed...))
}

// v4DecompressRead は reader から読み込み、v4 プロトコルのメッセージタイプに基づいて選択的に展開します。
func v4DecompressRead(reader Reader, compressor compress.Compressor) ([]byte, error) {
	bs, err := reader.Read()
	if err != nil {
		return nil, err
	}
	if len(bs) == 0 || bs[0] != byte(protocol.MessageTypeISCP) {
		return bs, nil
	}
	body, err := compressor.Decode(bs[1:])
	if err != nil {
		return nil, fmt.Errorf("decompress iSCP message: %w", err)
	}
	return append([]byte{bs[0]}, body...), nil
}

// NewV4Transport は v4 プロトコル用の選択的圧縮トランスポートを生成します。
// 圧縮が不要な場合（compressor が nil）は base をそのまま返します。
func NewV4Transport(base Transport, params NegotiationParams, compressConfig compress.Config) Transport {
	effectiveCfg := params.CompressConfig(compressConfig)
	comp := compress.NewCompressor(effectiveCfg)
	if comp == nil {
		return base
	}
	return &V4Transport{
		base:                 base,
		compressor:           comp,
		compressLevel:        effectiveCfg.Level,
		unreliableCompressor: compress.NewPerMessageCompressor(effectiveCfg.Level),
	}
}

// V4Transport は v4 プロトコル（ws2/quic2/webtrans2）のメッセージタイプバイトを認識し、
// iSCP メッセージ本体のみを選択的に圧縮/展開するトランスポートラッパーです。
type V4Transport struct {
	base                 Transport
	compressor           compress.Compressor
	compressLevel        int
	unreliableCompressor compress.Compressor
}

var (
	_ Transport        = (*V4Transport)(nil)
	_ MetricsSupporter = (*V4Transport)(nil)
)

func (t *V4Transport) Write(bs []byte) error {
	return v4CompressWrite(t.base, t.compressor, bs)
}

func (t *V4Transport) Read() ([]byte, error) {
	return v4DecompressRead(t.base, t.compressor)
}

// Close closes the transport.
func (t *V4Transport) Close() error {
	return t.base.Close()
}

// CloseWithStatus closes the transport with the given status.
func (t *V4Transport) CloseWithStatus(status CloseStatus) error {
	return t.base.CloseWithStatus(status)
}

// NegotiationParams returns the negotiation parameters.
func (t *V4Transport) NegotiationParams() NegotiationParams {
	return t.base.NegotiationParams()
}

// Name returns the transport name.
func (t *V4Transport) Name() Name {
	return t.base.Name()
}

// RxBytesCounterValue returns the total bytes received.
func (t *V4Transport) RxBytesCounterValue() uint64 {
	return t.base.RxBytesCounterValue()
}

// TxBytesCounterValue returns the total bytes sent.
func (t *V4Transport) TxBytesCounterValue() uint64 {
	return t.base.TxBytesCounterValue()
}

// AsUnreliable returns the unreliable transport wrapped with v4 compression.
// DATAGRAM は順序保証がないため、常に PerMessageCompressor を使用します。
func (t *V4Transport) AsUnreliable() (UnreliableTransport, bool) {
	ut, ok := t.base.AsUnreliable()
	if !ok {
		return nil, false
	}
	return &v4UnreliableTransport{base: ut, compressor: t.unreliableCompressor}, true
}

// MetricsProvider returns the metrics provider if the base transport supports it.
func (t *V4Transport) MetricsProvider() metrics.MetricsProvider {
	if ms, ok := t.base.(MetricsSupporter); ok {
		return ms.MetricsProvider()
	}
	return nil
}

// v4UnreliableTransport は UnreliableTransport に v4 圧縮ロジックを適用するラッパーです。
type v4UnreliableTransport struct {
	base       UnreliableTransport
	compressor compress.Compressor
}

var _ UnreliableTransport = (*v4UnreliableTransport)(nil)

func (t *v4UnreliableTransport) Write(bs []byte) error {
	return v4CompressWrite(t.base, t.compressor, bs)
}

func (t *v4UnreliableTransport) Read() ([]byte, error) {
	return v4DecompressRead(t.base, t.compressor)
}

func (t *v4UnreliableTransport) Close() error {
	return t.base.Close()
}

func (t *v4UnreliableTransport) RxBytesCounterValue() uint64 {
	return t.base.RxBytesCounterValue()
}

func (t *v4UnreliableTransport) TxBytesCounterValue() uint64 {
	return t.base.TxBytesCounterValue()
}

func (t *v4UnreliableTransport) IsUnreliable() {}
