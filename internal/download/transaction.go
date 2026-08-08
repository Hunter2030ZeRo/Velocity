package download

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

type fetchTransaction struct {
	created   map[string]os.FileInfo
	prefix    string
	progress  *progressTracker
	mu        sync.Mutex
	remaining atomic.Int64
}

type sharedFetch struct {
	transaction *fetchTransaction
	key         string
	artifact    parsedArtifact
}

// FetchAll fetches artifacts concurrently and returns their cache paths by lowercase digest.
func (f *Fetcher) FetchAll(ctx context.Context, artifacts []Artifact) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(artifacts) > f.maxArtifacts {
		return nil, fmt.Errorf("artifacts %d exceed limit %d: %w", len(artifacts), f.maxArtifacts, ErrArtifactLimit)
	}
	parsed := make([]parsedArtifact, 0, len(artifacts))
	digests := make(map[string]struct{}, len(artifacts))
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		value, err := f.parse(artifact)
		if err != nil {
			return nil, err
		}
		if _, exists := digests[value.digest]; exists {
			continue
		}
		digests[value.digest] = struct{}{}
		parsed = append(parsed, value)
	}
	batchID := f.batchID.Add(1)
	transaction := &fetchTransaction{
		prefix:   "batch-" + strconv.FormatUint(batchID, 10) + "-",
		created:  make(map[string]os.FileInfo),
		progress: newProgressTracker(f.progress, BatchID(batchID), len(parsed)),
	}
	transaction.remaining.Store(f.maxTotalBytes)
	paths, err := f.runTransaction(ctx, parsed, transaction)
	if err == nil {
		return paths, nil
	}
	if rollbackErr := transaction.rollback(); rollbackErr != nil {
		return nil, errors.Join(err, rollbackErr)
	}
	return nil, err
}

func (f *Fetcher) runTransaction(
	ctx context.Context,
	artifacts []parsedArtifact,
	transaction *fetchTransaction,
) (map[string]string, error) {
	group, groupCtx := errgroup.WithContext(ctx)
	paths := make(map[string]string, len(artifacts))
	var pathsMu sync.Mutex
	var next atomic.Int64
	workers := min(f.concurrency, len(artifacts))
	for range workers {
		group.Go(func() error {
			for {
				if err := groupCtx.Err(); err != nil {
					return err
				}
				index := int(next.Add(1) - 1)
				if index >= len(artifacts) {
					return nil
				}
				artifact := artifacts[index]
				result, err := f.fetchShared(groupCtx, sharedFetch{
					key:         transaction.prefix + artifact.digest,
					artifact:    artifact,
					transaction: transaction,
				})
				if err != nil {
					return fmt.Errorf("fetch %q: %w", artifact.url, err)
				}
				pathsMu.Lock()
				paths[artifact.digest] = result.path
				pathsMu.Unlock()
			}
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	return paths, nil
}

func (f *Fetcher) fetchShared(
	ctx context.Context,
	request sharedFetch,
) (cacheResult, error) {
	if err := ctx.Err(); err != nil {
		return cacheResult{}, err
	}
	value, err, _ := f.flights.Do(request.key, func() (any, error) {
		return f.fetch(ctx, request.artifact, request.transaction)
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return cacheResult{}, ctxErr
	}
	if err != nil {
		return cacheResult{}, err
	}
	result, ok := value.(cacheResult)
	if !ok {
		return cacheResult{}, errors.New("download: invalid singleflight result")
	}
	return result, nil
}

func (transaction *fetchTransaction) record(path string, info os.FileInfo) {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	transaction.created[path] = info
}

func (transaction *fetchTransaction) rollback() error {
	transaction.mu.Lock()
	defer transaction.mu.Unlock()
	var rollbackErr error
	for path, created := range transaction.created {
		current, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("inspect rollback cache entry: %w", err))
			continue
		}
		if !os.SameFile(created, current) {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback cache entry changed: %w", ErrUnsafeCache))
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("rollback cache entry: %w", removeErr))
		}
	}
	return rollbackErr
}
