package extract

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"
)

// ExtractAll extracts jobs into stageDir without overwriting existing files.
func (e *Extractor) ExtractAll(ctx context.Context, stageDir string, jobs []Job) (_ []Result, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context: %w", ErrInvalidJob)
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	info, err := os.Stat(stageDir)
	if err != nil {
		return nil, fmt.Errorf("stat stage directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("stage path is not a directory: %w", ErrInvalidJob)
	}
	workDir, err := os.MkdirTemp(stageDir, ".extract-")
	if err != nil {
		return nil, fmt.Errorf("create extraction workspace: %w", err)
	}
	defer func() {
		err = errors.Join(err, removeWorkspace(workDir))
	}()

	plans, err := validateJobs(stageDir, workDir, jobs)
	if err != nil {
		return nil, err
	}
	for _, plan := range plans {
		for _, binary := range plan.binaries {
			if _, statErr := os.Lstat(binary.finalPath); statErr == nil {
				return nil, fmt.Errorf("destination %q: %w", binary.name, ErrDestinationExists)
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return nil, fmt.Errorf("inspect destination %q: %w", binary.name, statErr)
			}
		}
	}

	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(e.concurrency)
	for _, plan := range plans {
		group.Go(func() error {
			if extractErr := e.extractJob(groupCtx, plan); extractErr != nil {
				return fmt.Errorf("extract %q: %w", plan.job.ArchivePath, extractErr)
			}
			return nil
		})
	}
	if waitErr := group.Wait(); waitErr != nil {
		return nil, waitErr
	}
	return commit(plans)
}

func commit(plans []jobPlan) ([]Result, error) {
	resultCount := 0
	for _, plan := range plans {
		resultCount += len(plan.binaries)
	}
	results := make([]Result, resultCount)
	committed := make([]string, 0, resultCount)
	for _, plan := range plans {
		for _, binary := range plan.binaries {
			if err := os.Link(binary.tempPath, binary.finalPath); err != nil {
				cleanupErr := removeCommitted(committed)
				return nil, errors.Join(fmt.Errorf("commit %q: %w", binary.name, err), cleanupErr)
			}
			committed = append(committed, binary.finalPath)
			results[binary.index] = Result{Name: binary.name, Path: binary.finalPath}
		}
	}
	return results, nil
}

func removeCommitted(paths []string) error {
	var cleanupErr error
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove committed %q: %w", path, err))
		}
	}
	return cleanupErr
}

func removeWorkspace(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove extraction workspace %q: %w", filepath.Base(path), err)
	}
	return nil
}
