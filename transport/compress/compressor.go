package compress

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/flate"
)

// Compressor は DEFLATE 圧縮/展開を行うインターフェースです。
type Compressor interface {
	// Encode は入力データを圧縮します。
	Encode([]byte) ([]byte, error)
	// Decode は圧縮データを展開します。
	Decode([]byte) ([]byte, error)
}

// NewCompressor は Config に基づいて適切な Compressor を返します。
// 圧縮が無効の場合は nil を返します。
func NewCompressor(cfg Config) Compressor {
	if !cfg.Enable {
		return nil
	}
	if cfg.DisableContextTakeover {
		return NewPerMessageCompressor(cfg.Level)
	}
	return NewContextTakeoverCompressor(cfg.Level, cfg.WindowSize())
}

// PerMessageCompressor はメッセージごとに独立して DEFLATE 圧縮を行います。
type PerMessageCompressor struct {
	level int
}

// NewPerMessageCompressor creates a new per-message compressor with the given compression level.
func NewPerMessageCompressor(level int) *PerMessageCompressor {
	return &PerMessageCompressor{level: level}
}

// Encode compresses the input data using DEFLATE.
func (c *PerMessageCompressor) Encode(bs []byte) ([]byte, error) {
	return Encode(bs, c.level)
}

// Decode decompresses DEFLATE-compressed data.
func (c *PerMessageCompressor) Decode(bs []byte) ([]byte, error) {
	return Decode(bs)
}

// ContextTakeoverCompressor はメッセージを跨いで DEFLATE 圧縮コンテキストを引き継ぎます。
// LZ77 スライディングウィンドウを保持し、連続するメッセージ間で圧縮効率を高めます。
type ContextTakeoverCompressor struct {
	level      int
	windowSize int

	writeWindowBuf   *bytes.Buffer
	writeWindowBufMu sync.Mutex

	readWindowBuf   *bytes.Buffer
	readWindowBufMu sync.Mutex
}

// NewContextTakeoverCompressor creates a new context-takeover compressor.
// The windowSize parameter specifies the sliding window size in bytes.
func NewContextTakeoverCompressor(level, windowSize int) *ContextTakeoverCompressor {
	return &ContextTakeoverCompressor{
		level:          level,
		windowSize:     windowSize,
		writeWindowBuf: bytes.NewBuffer(nil),
		readWindowBuf:  bytes.NewBuffer(nil),
	}
}

// Encode compresses data using DEFLATE with context takeover.
// The sliding window from previous messages is used as a dictionary.
func (c *ContextTakeoverCompressor) Encode(bs []byte) ([]byte, error) {
	c.writeWindowBufMu.Lock()
	defer c.writeWindowBufMu.Unlock()

	var buf bytes.Buffer
	fwr, err := flate.NewWriterDict(&buf, c.level, c.writeWindowBuf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("new flate writer dict: %w", err)
	}

	// 入力データをフラットライターとウィンドウバッファの両方に書き込む
	mwr := io.MultiWriter(fwr, c.writeWindowBuf)
	if _, err := mwr.Write(bs); err != nil {
		return nil, fmt.Errorf("write data: %w", err)
	}
	if err := fwr.Flush(); err != nil {
		return nil, fmt.Errorf("flush: %w", err)
	}
	if err := fwr.Close(); err != nil {
		return nil, fmt.Errorf("close: %w", err)
	}

	// ウィンドウサイズを超えた分をトリム
	if c.windowSize < c.writeWindowBuf.Len() {
		c.writeWindowBuf.Next(c.writeWindowBuf.Len() - c.windowSize)
	}

	return buf.Bytes(), nil
}

// Decode decompresses DEFLATE data with context takeover.
// The sliding window from previous messages is used as a dictionary.
func (c *ContextTakeoverCompressor) Decode(bs []byte) ([]byte, error) {
	c.readWindowBufMu.Lock()
	defer c.readWindowBufMu.Unlock()

	inputBuf := bytes.NewBuffer(bs)
	frd := flate.NewReaderDict(inputBuf, c.readWindowBuf.Bytes())

	var decompressedBuf bytes.Buffer
	trd := io.TeeReader(frd, c.readWindowBuf)
	if _, err := io.Copy(&decompressedBuf, trd); err != nil {
		return nil, fmt.Errorf("decompress data: %w", err)
	}

	// ウィンドウサイズを超えた分をトリム
	if c.windowSize < c.readWindowBuf.Len() {
		c.readWindowBuf.Next(c.readWindowBuf.Len() - c.windowSize)
	}

	if err := frd.Close(); err != nil {
		return nil, fmt.Errorf("close flate reader: %w", err)
	}

	return decompressedBuf.Bytes(), nil
}
