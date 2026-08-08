// Package extract safely materializes explicitly mapped binaries from archives.
package extract

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const (
	maxZipEntries          = 10_000
	maxTarMetadataBytes    = 1 << 20
	zstdDecoderMemoryLimit = 64 << 20
	zstdDecoderWindowLimit = 64 << 20
)

func (e *Extractor) extractJob(ctx context.Context, plan jobPlan) error {
	switch plan.job.Archive {
	case "zip":
		return e.extractZip(ctx, plan)
	case "tar.gz":
		return e.extractGzip(ctx, plan)
	case "tar.zst":
		return e.extractZstd(ctx, plan)
	case "raw":
		return e.extractRaw(ctx, plan)
	default:
		return ErrUnsupportedArchive
	}
}

func (e *Extractor) extractZip(ctx context.Context, plan jobPlan) (err error) {
	archive, err := openBoundedZIP(ctx, plan.job.ArchivePath)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, archive.Close()) }()
	found := make(map[string]bool, len(plan.binaries))
	budget := newArchiveBudget(e.maxArchiveBytes)
	for _, entry := range archive.File {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if !safeArchivePath(entry.Name) {
			return fmt.Errorf("entry %q: %w", entry.Name, ErrUnsafeArchive)
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %q: %w", entry.Name, ErrUnsafeArchive)
		}
		stripped, retained := strippedPath(entry.Name, plan.job.StripComponents)
		if !retained || entry.FileInfo().IsDir() {
			continue
		}
		binary, mapped := plan.bySource[stripped]
		if !mapped {
			continue
		}
		if found[binary.source] {
			return fmt.Errorf("archive source %q: %w", binary.source, ErrDuplicateSource)
		}
		if !budget.permitsUint64(entry.UncompressedSize64) {
			return ErrArchiveTooLarge
		}
		reader, openErr := entry.Open()
		if openErr != nil {
			return fmt.Errorf("open entry %q: %w", entry.Name, openErr)
		}
		copyErr := e.copyBinary(ctx, newBudgetReader(reader, budget), binary.tempPath)
		closeErr := reader.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
		found[binary.source] = true
	}
	return ensureFound(plan.binaries, found)
}

func (e *Extractor) extractGzip(ctx context.Context, plan jobPlan) (err error) {
	file, err := os.Open(plan.job.ArchivePath)
	if err != nil {
		return fmt.Errorf("open tar.gz: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer func() { err = errors.Join(err, reader.Close()) }()
	return e.extractTar(ctx, reader, plan)
}

func (e *Extractor) extractZstd(ctx context.Context, plan jobPlan) (err error) {
	file, err := os.Open(plan.job.ArchivePath)
	if err != nil {
		return fmt.Errorf("open tar.zst: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	reader, err := zstd.NewReader(
		file,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(zstdDecoderMemoryLimit),
		zstd.WithDecoderMaxWindow(zstdDecoderWindowLimit),
	)
	if err != nil {
		return fmt.Errorf("open zstd stream: %w", err)
	}
	defer reader.Close()
	return e.extractTar(ctx, reader, plan)
}

func (e *Extractor) extractTar(ctx context.Context, source io.Reader, plan jobPlan) error {
	expandedLimit := e.maxArchiveBytes
	if expandedLimit <= math.MaxInt64-maxTarMetadataBytes {
		expandedLimit += maxTarMetadataBytes
	} else {
		expandedLimit = math.MaxInt64
	}
	reader := tar.NewReader(newExpandedReader(ctx, source, expandedLimit))
	found := make(map[string]bool, len(plan.binaries))
	budget := newArchiveBudget(e.maxArchiveBytes)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if validateErr := validateTarHeader(header); validateErr != nil {
			return validateErr
		}
		stripped, retained := strippedPath(header.Name, plan.job.StripComponents)
		if !retained || header.FileInfo().IsDir() {
			continue
		}
		if !budget.permits(header.Size) {
			return ErrArchiveTooLarge
		}
		binary, mapped := plan.bySource[stripped]
		if !mapped {
			budget.consume(header.Size)
			continue
		}
		if found[binary.source] {
			return fmt.Errorf("archive source %q: %w", binary.source, ErrDuplicateSource)
		}
		if copyErr := e.copyBinary(ctx, newBudgetReader(reader, budget), binary.tempPath); copyErr != nil {
			return copyErr
		}
		found[binary.source] = true
	}
	return ensureFound(plan.binaries, found)
}

func validateTarHeader(header *tar.Header) error {
	if !safeArchivePath(header.Name) {
		return fmt.Errorf("entry %q: %w", header.Name, ErrUnsafeArchive)
	}
	if isSparseTarHeader(header) {
		return fmt.Errorf("sparse entry %q: %w", header.Name, ErrUnsafeArchive)
	}
	if header.Typeflag != tar.TypeDir && !isRegularTarType(header.Typeflag) {
		return fmt.Errorf("non-regular entry %q: %w", header.Name, ErrUnsafeArchive)
	}
	return nil
}

func isRegularTarType(typeFlag byte) bool {
	return typeFlag == tar.TypeReg || typeFlag == 0
}

func isSparseTarHeader(header *tar.Header) bool {
	if header.Typeflag == tar.TypeGNUSparse {
		return true
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return true
		}
	}
	return false
}

func (e *Extractor) extractRaw(ctx context.Context, plan jobPlan) (err error) {
	file, err := os.Open(plan.job.ArchivePath)
	if err != nil {
		return fmt.Errorf("open raw artifact: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return e.copyBinary(ctx, file, plan.binaries[0].tempPath)
}

func ensureFound(binaries []binaryPlan, found map[string]bool) error {
	for _, binary := range binaries {
		if !found[binary.source] {
			return fmt.Errorf("source %q: %w", binary.source, ErrMissingBinary)
		}
	}
	return nil
}
