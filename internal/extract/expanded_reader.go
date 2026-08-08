package extract

import (
	"context"
	"io"
)

type expandedReader struct {
	ctx       context.Context
	reader    io.Reader
	remaining int64
}

func newExpandedReader(ctx context.Context, reader io.Reader, maxBytes int64) *expandedReader {
	return &expandedReader{ctx: ctx, reader: reader, remaining: maxBytes}
}

func (r *expandedReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.remaining < int64(len(buffer)) {
		buffer = buffer[:r.remaining+1]
	}
	read, err := r.reader.Read(buffer)
	r.remaining -= int64(read)
	if r.remaining < 0 {
		return read, ErrArchiveTooLarge
	}
	return read, err
}
