package extract

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_rejects_advertised_zip_count_before_opening_directory(t *testing.T) {
	for _, zip64 := range []bool{false, true} {
		name := "ordinary"
		if zip64 {
			name = "zip64"
		}
		t.Run(name, func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			archivePath := filepath.Join(t.TempDir(), "oversized.zip")
			require.NoError(t, os.WriteFile(archivePath, advertisedZIPCount(10_001, zip64), 0o600))
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
			require.NoError(t, err)
			job := Job{ArchivePath: archivePath, Archive: "zip", Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.ErrorIs(t, err, ErrArchiveTooLarge)
			require.Nil(t, results)
			require.Empty(t, readDirNames(t, stageDir))
		})
	}
}

func Test_ExtractAll_rejects_zip_count_modulo_mismatch_before_opening_directory(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	archivePath := filepath.Join(t.TempDir(), "modulo-count.zip")
	require.NoError(t, os.WriteFile(archivePath, zipWithModuloEntryCount(65_537), 0o600))
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
	require.NoError(t, err)
	job := Job{ArchivePath: archivePath, Archive: "zip", Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, ErrArchiveTooLarge)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}

func Test_ExtractAll_rejects_zip_metadata_bytes_before_opening_directory(t *testing.T) {
	for _, zip64 := range []bool{false, true} {
		name := "ordinary"
		if zip64 {
			name = "zip64"
		}
		t.Run(name, func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			archivePath := writeSparseMetadataZIP(t, zip64)
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
			require.NoError(t, err)
			job := Job{ArchivePath: archivePath, Archive: "zip", Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.ErrorIs(t, err, ErrArchiveTooLarge)
			require.Nil(t, results)
			require.Empty(t, readDirNames(t, stageDir))
		})
	}
}

func writeSparseMetadataZIP(t *testing.T, zip64 bool) string {
	t.Helper()
	const (
		entries    int64 = 342
		metadata         = 3 * math.MaxUint16
		recordSize       = zipDirectoryHeaderSize + metadata
	)
	path := filepath.Join(t.TempDir(), "metadata.zip")
	file, err := os.Create(path)
	require.NoError(t, err)
	var header [zipDirectoryHeaderSize]byte
	binary.LittleEndian.PutUint32(header[:], zipDirectoryHeaderSignature)
	binary.LittleEndian.PutUint16(header[28:], math.MaxUint16)
	binary.LittleEndian.PutUint16(header[30:], math.MaxUint16)
	binary.LittleEndian.PutUint16(header[32:], math.MaxUint16)
	for index := range entries {
		_, err = file.WriteAt(header[:], index*recordSize)
		require.NoError(t, err)
	}
	directorySize := entries * recordSize
	end := advertisedZIPCount(342, zip64)
	if zip64 {
		binary.LittleEndian.PutUint64(end[40:], uint64(directorySize))
		binary.LittleEndian.PutUint64(end[64:], uint64(directorySize))
	} else {
		binary.LittleEndian.PutUint32(end[12:], uint32(directorySize))
	}
	_, err = file.WriteAt(end, directorySize)
	require.NoError(t, err)
	require.NoError(t, file.Truncate(directorySize+int64(len(end))))
	require.NoError(t, file.Close())
	return path
}

func Test_openBoundedZIP_rejects_modulo_count_without_entry_allocations(t *testing.T) {
	// Given
	archivePath := filepath.Join(t.TempDir(), "modulo-count.zip")
	require.NoError(t, os.WriteFile(archivePath, zipWithModuloEntryCount(65_537), 0o600))
	var observedErr error

	// When
	allocations := testing.AllocsPerRun(1, func() {
		archive, err := openBoundedZIP(context.Background(), archivePath)
		observedErr = err
		if archive != nil {
			if closeErr := archive.Close(); closeErr != nil && observedErr == nil {
				observedErr = closeErr
			}
		}
	})

	// Then
	require.ErrorIs(t, observedErr, ErrArchiveTooLarge)
	require.Less(t, allocations, 1_000.0)
}

func zipWithModuloEntryCount(entries int) []byte {
	const directoryHeaderSize = 46
	directorySize := directoryHeaderSize * entries
	contents := make([]byte, directorySize+zipEndSize)
	for offset := 0; offset < directorySize; offset += directoryHeaderSize {
		binary.LittleEndian.PutUint32(contents[offset:], 0x02014b50)
	}
	end := contents[directorySize:]
	binary.LittleEndian.PutUint32(end, zipEndSignature)
	binary.LittleEndian.PutUint16(end[8:], uint16(entries))
	binary.LittleEndian.PutUint16(end[10:], uint16(entries))
	binary.LittleEndian.PutUint32(end[12:], uint32(directorySize))
	return contents
}

func Test_preflightZIP_rejects_malformed_end_records(t *testing.T) {
	overflow := advertisedZIPCount(1, true)
	binary.LittleEndian.PutUint64(overflow[64:], math.MaxUint64)
	multiDisk := advertisedZIPCount(1, false)
	binary.LittleEndian.PutUint16(multiDisk[4:], 1)
	malformedComment := append(advertisedZIPCount(1, false), 0)
	fixtures := map[string][]byte{
		"truncated":         {0x50, 0x4b, 0x05},
		"multi disk":        multiDisk,
		"zip64 overflow":    overflow,
		"malformed comment": malformedComment,
	}
	for name, contents := range fixtures {
		t.Run(name, func(t *testing.T) {
			// Given
			path := filepath.Join(t.TempDir(), "malformed.zip")
			require.NoError(t, os.WriteFile(path, contents, 0o600))

			// When
			err := preflightZIP(context.Background(), path)

			// Then
			require.ErrorIs(t, err, zip.ErrFormat)
		})
	}
}

func Test_preflightZIP_cancellation_interrupts_central_directory_scan(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "large-directory.zip")
	require.NoError(t, os.WriteFile(path, zipWithModuloEntryCount(10_000), 0o600))
	ctx := newCheckCancelContext(128)

	// When
	err := preflightZIP(ctx, path)

	// Then
	require.ErrorIs(t, err, context.Canceled)
	require.LessOrEqual(t, ctx.checks, ctx.limit+1)
}

type checkCancelContext struct {
	done   chan struct{}
	limit  int
	checks int
}

func newCheckCancelContext(limit int) *checkCancelContext {
	return &checkCancelContext{done: make(chan struct{}), limit: limit}
}

func (c *checkCancelContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *checkCancelContext) Done() <-chan struct{}       { return c.done }
func (c *checkCancelContext) Value(any) any               { return nil }

func (c *checkCancelContext) Err() error {
	c.checks++
	if c.checks >= c.limit {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return context.Canceled
	}
	return nil
}

func advertisedZIPCount(entries uint64, zip64 bool) []byte {
	if !zip64 {
		end := make([]byte, 22)
		binary.LittleEndian.PutUint32(end, 0x06054b50)
		binary.LittleEndian.PutUint16(end[8:], uint16(entries))
		binary.LittleEndian.PutUint16(end[10:], uint16(entries))
		return end
	}
	record := make([]byte, 56+20+22)
	binary.LittleEndian.PutUint32(record, 0x06064b50)
	binary.LittleEndian.PutUint64(record[4:], 44)
	binary.LittleEndian.PutUint64(record[24:], entries)
	binary.LittleEndian.PutUint64(record[32:], entries)
	locator := record[56:]
	binary.LittleEndian.PutUint32(locator, 0x07064b50)
	binary.LittleEndian.PutUint32(locator[16:], 1)
	end := record[76:]
	binary.LittleEndian.PutUint32(end, 0x06054b50)
	binary.LittleEndian.PutUint16(end[8:], 0xffff)
	binary.LittleEndian.PutUint16(end[10:], 0xffff)
	binary.LittleEndian.PutUint32(end[12:], 0xffffffff)
	binary.LittleEndian.PutUint32(end[16:], 0xffffffff)
	return record
}
