package transport

import (
	"fmt"

	"github.com/aptpod/iscp-go/errors"
	"github.com/aptpod/iscp-go/transport/metrics"
)

// Readerはトランスポートからメッセージを読み出すインターフェースです。
type Reader interface {
	// Read は、トランスポートからメッセージを読み出します。
	Read() ([]byte, error)
	// Close は、トランスポートのコネクションを切断します。
	Close() error
	// RxBytesCounterValue は、現在の受信メッセージカウンターの値を返します。
	RxBytesCounterValue() uint64
}

// Writerはトランスポートからメッセージを書き込むインターフェースです。
type Writer interface {
	// Write は、トランスポートへメッセージを書き込みます。
	Write([]byte) error
	// Close は、トランスポートのコネクションを切断します。
	Close() error
	// TxBytesCounterValue は、現在の送信バイトカウンターの値を返します。
	TxBytesCounterValue() uint64
}

// ReadWriterはトランスポートからメッセージを読み書きのインターフェースです。
type ReadWriter interface {
	Reader
	Writer
}

/*
Transport は、 iSCP のトランスポート層を抽象化したインターフェースです。
*/
type Transport interface {
	ReadWriter

	// AsUnreliable は UnreliableTransportを返します。
	//
	// もし、 Unreliableをサポートしていない場合は okはfalseを返します。
	AsUnreliable() (tr UnreliableTransport, ok bool)

	// NegotiationParams は、トランスポートで事前ネゴシエーションされたパラメーターを返します。
	NegotiationParams() NegotiationParams

	// Nameは、トランスポート名を返却します。
	Name() Name
}

type UnreliableTransport interface {
	ReadWriter
	IsUnreliable()
}

type CloseStatus string

const (
	CloseStatusNormal        CloseStatus = "normal"
	CloseStatusAbnormal      CloseStatus = "abnormal"
	CloseStatusGoingAway     CloseStatus = "goingaway"
	CloseStatusInternalError CloseStatus = "internal"
)

type Closer interface {
	Close() error
	CloseWithStatus(CloseStatus) error
}

// MetricsSupporter は、メトリクス取得機能を持つトランスポートのインターフェースです。
// トランスポートがメトリクス情報（RTT、CWND、BytesInFlight等）を提供できる場合、このインターフェースを実装します。
type MetricsSupporter interface {
	// MetricsProvider は、トランスポートのメトリクスを提供するプロバイダーを返します。
	// メトリクスが利用できない場合はnilを返します。
	MetricsProvider() metrics.MetricsProvider
}

// GetCloseStatusError は CloseStatus に応じたエラーを返します
func GetCloseStatusError(status CloseStatus) error {
	switch status {
	case CloseStatusNormal:
		return errors.ErrConnectionNormalClose
	case CloseStatusAbnormal:
		return errors.ErrConnectionAbnormalClose
	case CloseStatusGoingAway:
		return errors.ErrConnectionGoingAwayClose
	case CloseStatusInternalError:
		return errors.ErrConnectionInternalErrorClose
	default:
		return fmt.Errorf("unknown close status %s: %w", status, errors.ErrConnectionClosed)
	}
}

// GetCloseStatus は エラーに 応じたステータスを返します
func GetCloseStatus(err error) CloseStatus {
	if errors.Is(err, errors.ErrConnectionNormalClose) {
		return CloseStatusNormal
	}
	if errors.Is(err, errors.ErrConnectionAbnormalClose) {
		return CloseStatusAbnormal
	}
	if errors.Is(err, errors.ErrConnectionGoingAwayClose) {
		return CloseStatusGoingAway
	}
	if errors.Is(err, errors.ErrConnectionInternalErrorClose) {
		return CloseStatusInternalError
	}
	return CloseStatusInternalError
}
