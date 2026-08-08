package download

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
)

type cacheResult struct {
	path string
}

type cacheWrite struct {
	transaction *fetchTransaction
	source      io.Reader
	artifact    parsedArtifact
}

type cacheValidation struct {
	transaction *fetchTransaction
	path        string
	expected    []byte
}

func (f *Fetcher) fetch(
	ctx context.Context,
	artifact parsedArtifact,
	transaction *fetchTransaction,
) (cacheResult, error) {
	path, cached, err := f.prepareCache(ctx, artifact, transaction)
	if err != nil {
		return cacheResult{}, err
	}
	if cached {
		return cacheResult{path: path}, nil
	}
	return f.download(ctx, artifact, transaction)
}

func (f *Fetcher) prepareCache(
	ctx context.Context,
	artifact parsedArtifact,
	transaction *fetchTransaction,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if err := os.MkdirAll(f.cacheDir, 0o700); err != nil {
		return "", false, fmt.Errorf("create cache directory: %w", err)
	}
	if err := validateCacheDirectory(f.cacheDir); err != nil {
		return "", false, err
	}
	path := filepath.Join(f.cacheDir, artifact.digest)
	valid, err := f.validCache(ctx, cacheValidation{
		path:        path,
		expected:    artifact.expected[:],
		transaction: transaction,
	})
	if err != nil {
		return "", false, err
	}
	if valid {
		return path, true, nil
	}
	if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("remove corrupt cache entry: %w", removeErr)
	}
	return path, false, nil
}

func (f *Fetcher) download(
	ctx context.Context,
	artifact parsedArtifact,
	transaction *fetchTransaction,
) (result cacheResult, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.url.String(), nil)
	if err != nil {
		return cacheResult{}, fmt.Errorf("build request: %w", err)
	}
	response, err := f.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return cacheResult{}, ctx.Err()
		}
		return cacheResult{}, fmt.Errorf("download %s: %w", artifact.url, err)
	}
	if response == nil || response.Body == nil {
		return cacheResult{}, errors.New("download: HTTP client returned an empty response")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close download response: %w", closeErr)
		}
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return cacheResult{}, fmt.Errorf("download %s returned HTTP %d", artifact.url, response.StatusCode)
	}
	return f.writeCache(ctx, cacheWrite{
		source:      response.Body,
		artifact:    artifact,
		transaction: transaction,
	})
}

func (f *Fetcher) writeCache(ctx context.Context, write cacheWrite) (result cacheResult, err error) {
	tempFile, err := os.CreateTemp(f.cacheDir, "."+write.artifact.digest+"-*")
	if err != nil {
		return cacheResult{}, fmt.Errorf("create cache temporary file: %w", err)
	}
	temp := &cacheTemp{file: tempFile, name: tempFile.Name(), cleanup: true}
	defer temp.finish(&err)
	hash := sha256.New()
	var destination io.Writer
	destination = io.MultiWriter(temp.file, hash)
	if write.transaction != nil {
		destination = &budgetWriter{remaining: &write.transaction.remaining, destination: destination}
	}
	written, err := io.Copy(
		destination,
		io.LimitReader(contextReader{ctx: ctx, source: write.source}, f.maxBytes+1),
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return cacheResult{}, ctxErr
		}
		return cacheResult{}, fmt.Errorf("stream artifact: %w", err)
	}
	if written > f.maxBytes {
		return cacheResult{}, ErrTooLarge
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), write.artifact.expected[:]) != 1 {
		return cacheResult{}, ErrChecksum
	}
	path := filepath.Join(f.cacheDir, write.artifact.digest)
	created, publishErr := temp.publish(path)
	if publishErr != nil {
		return cacheResult{}, publishErr
	}
	if !created {
		valid, validationErr := f.validCache(ctx, cacheValidation{
			path:        path,
			expected:    write.artifact.expected[:],
			transaction: write.transaction,
		})
		if validationErr != nil {
			return cacheResult{}, validationErr
		}
		if !valid {
			return cacheResult{}, ErrChecksum
		}
		return cacheResult{path: path}, nil
	}
	if write.transaction != nil {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return cacheResult{}, fmt.Errorf("inspect committed cache entry: %w", statErr)
		}
		write.transaction.record(path, info)
	}
	return cacheResult{path: path}, nil
}

func (f *Fetcher) validCache(ctx context.Context, validation cacheValidation) (valid bool, err error) {
	entry, err := os.Lstat(validation.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect cache entry: %w", err)
	}
	if validationErr := validateCacheEntry(validation.path, entry); validationErr != nil {
		return false, validationErr
	}
	file, err := os.Open(validation.path)
	if err != nil {
		return false, fmt.Errorf("open cache entry: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close cache entry: %w", closeErr)
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect opened cache entry: %w", err)
	}
	if !os.SameFile(entry, opened) {
		return false, fmt.Errorf("cache entry changed while opening: %w", ErrUnsafeCache)
	}
	hash := sha256.New()
	var destination io.Writer
	destination = hash
	if validation.transaction != nil {
		destination = &budgetWriter{
			remaining:   &validation.transaction.remaining,
			destination: destination,
		}
	}
	written, err := io.Copy(
		destination,
		io.LimitReader(contextReader{ctx: ctx, source: file}, f.maxBytes+1),
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, fmt.Errorf("verify cache entry: %w", err)
	}
	return written <= f.maxBytes && subtle.ConstantTimeCompare(hash.Sum(nil), validation.expected) == 1, nil
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}

type budgetWriter struct {
	remaining   *atomic.Int64
	destination io.Writer
}

func (writer *budgetWriter) Write(buffer []byte) (int, error) {
	requested := int64(len(buffer))
	for {
		remaining := writer.remaining.Load()
		if requested > remaining {
			return 0, ErrFetchLimit
		}
		if writer.remaining.CompareAndSwap(remaining, remaining-requested) {
			return writer.destination.Write(buffer)
		}
	}
}
