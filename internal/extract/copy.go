package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

func (e *Extractor) copyBinary(ctx context.Context, source io.Reader, destination string) (err error) {
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create staged binary: %w", err)
	}
	defer func() {
		err = errors.Join(err, file.Close())
	}()

	buffer := make([]byte, 32*1024)
	var copied int64
	for {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		read, readErr := source.Read(buffer)
		if int64(read) > e.maxBinaryBytes-copied {
			return ErrBinaryTooLarge
		}
		if read > 0 {
			written, writeErr := file.Write(buffer[:read])
			if writeErr != nil {
				return fmt.Errorf("write staged binary: %w", writeErr)
			}
			if written != read {
				return io.ErrShortWrite
			}
			copied += int64(written)
		}
		if errors.Is(readErr, io.EOF) {
			if chmodErr := file.Chmod(0o755); chmodErr != nil {
				return fmt.Errorf("mark staged binary executable: %w", chmodErr)
			}
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("read binary: %w", readErr)
		}
	}
}
