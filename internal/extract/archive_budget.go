package extract

import (
	"io"
	"math"
)

type archiveBudget struct {
	remaining int64
}

func newArchiveBudget(maxBytes int64) *archiveBudget {
	return &archiveBudget{remaining: maxBytes}
}

func (b *archiveBudget) permits(size int64) bool {
	return size >= 0 && size <= b.remaining
}

func (b *archiveBudget) permitsUint64(size uint64) bool {
	return size <= math.MaxInt64 && b.permits(int64(size))
}

func (b *archiveBudget) consume(size int64) {
	b.remaining -= size
}

type budgetReader struct {
	reader io.Reader
	budget *archiveBudget
}

func newBudgetReader(reader io.Reader, budget *archiveBudget) *budgetReader {
	return &budgetReader{reader: reader, budget: budget}
}

func (r *budgetReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if r.budget.remaining == 0 {
		var extra [1]byte
		read, err := r.reader.Read(extra[:])
		if read > 0 {
			return 0, ErrArchiveTooLarge
		}
		return 0, err
	}
	if int64(len(buffer)) > r.budget.remaining {
		buffer = buffer[:r.budget.remaining]
	}
	read, err := r.reader.Read(buffer)
	r.budget.consume(int64(read))
	return read, err
}
