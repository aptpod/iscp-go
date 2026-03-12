package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// LengthPrefixSize は長さプレフィックスのバイト数です。
	LengthPrefixSize = 4
	// DefaultMaxChunkSize はデフォルトの最大チャンクサイズ (8KB) です。
	DefaultMaxChunkSize = 8 * 1024
)

// EncodeLengthPrefix はメッセージ長を 4-byte Big-Endian uint32 にエンコードします。
func EncodeLengthPrefix(length uint32) []byte {
	bs := make([]byte, LengthPrefixSize)
	binary.BigEndian.PutUint32(bs, length)
	return bs
}

// DecodeLengthPrefix は 4-byte Big-Endian uint32 からメッセージ長をデコードします。
func DecodeLengthPrefix(data []byte) (uint32, error) {
	if len(data) < LengthPrefixSize {
		return 0, fmt.Errorf("data too short for length prefix: need %d bytes, got %d", LengthPrefixSize, len(data))
	}
	return binary.BigEndian.Uint32(data[:LengthPrefixSize]), nil
}

// FrameMessage はメッセージに長さプレフィックスを付与したフレームを作成します。
func FrameMessage(msg []byte) []byte {
	framed := make([]byte, LengthPrefixSize+len(msg))
	binary.BigEndian.PutUint32(framed[:LengthPrefixSize], uint32(len(msg)))
	copy(framed[LengthPrefixSize:], msg)
	return framed
}

// SplitIntoChunks はデータを maxChunkSize ごとに分割します。
// maxChunkSize が 0 以下の場合は DefaultMaxChunkSize を使用します。
func SplitIntoChunks(data []byte, maxChunkSize int) [][]byte {
	if maxChunkSize <= 0 {
		maxChunkSize = DefaultMaxChunkSize
	}
	if len(data) == 0 {
		return nil
	}
	chunks := make([][]byte, 0, (len(data)+maxChunkSize-1)/maxChunkSize)
	for offset := 0; offset < len(data); offset += maxChunkSize {
		end := min(offset+maxChunkSize, len(data))
		chunks = append(chunks, data[offset:end])
	}
	return chunks
}

// WriteTo は長さプレフィックス付きメッセージを io.Writer に書き込みます。
// QUIC/WebTransport のストリーム書き込みに使用します。
func WriteTo(w io.Writer, payload []byte) (int, error) {
	prefix := EncodeLengthPrefix(uint32(len(payload)))
	if _, err := w.Write(prefix); err != nil {
		return 0, err
	}
	if _, err := w.Write(payload); err != nil {
		return 0, err
	}
	return LengthPrefixSize + len(payload), nil
}

// ReadFrom は io.Reader から長さプレフィックス付きメッセージを読み取ります。
// QUIC/WebTransport のストリーム読み取りに使用します。
func ReadFrom(r io.Reader) ([]byte, error) {
	prefix := make([]byte, LengthPrefixSize)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return nil, err
	}
	msgLength := binary.BigEndian.Uint32(prefix)

	payload := make([]byte, msgLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}
