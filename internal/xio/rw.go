package xio

import "io"

type CaptureReader struct {
	ReadBytes int
	io.Reader
}

func NewCaptureReader(rd io.Reader) *CaptureReader {
	return &CaptureReader{
		Reader: rd,
	}
}

func (r *CaptureReader) Read(bs []byte) (int, error) {
	n, err := r.Reader.Read(bs)
	r.ReadBytes += n
	return n, err
}
