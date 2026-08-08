package extract

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_rejects_zip_when_mapped_entries_exceed_aggregate_budget(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 10})
	require.NoError(t, err)
	archivePath := writeZip(t, []testEntry{{name: "bin/a", body: "123456"}, {name: "bin/b", body: "abcdef"}})
	job := Job{ArchivePath: archivePath, Archive: "zip", Binaries: []Binary{
		{Source: "bin/a", Name: "a"}, {Source: "bin/b", Name: "b"},
	}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, ErrArchiveTooLarge)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}

func Test_ExtractAll_rejects_zip_with_excessive_member_count(t *testing.T) {
	// Given
	const excessiveMemberCount = 10_001
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
	require.NoError(t, err)
	entries := make([]testEntry, excessiveMemberCount)
	entries[0] = testEntry{name: "bin/tool", body: "tool"}
	for index := 1; index < len(entries); index++ {
		entries[index] = testEntry{name: fmt.Sprintf("docs/%05d", index)}
	}
	archivePath := writeZip(t, entries)
	job := Job{ArchivePath: archivePath, Archive: "zip", Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, ErrArchiveTooLarge)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}

func Test_ExtractAll_rejects_pax_sparse_mapped_entry(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 2 << 20, MaxArchiveBytes: 2 << 20})
	require.NoError(t, err)
	archivePath := writePAXSparseTarGzip(t)
	job := Job{ArchivePath: archivePath, Archive: "tar.gz", Binaries: []Binary{{Source: "sparse.db", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, ErrUnsafeArchive)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}

func writePAXSparseTarGzip(t *testing.T) string {
	t.Helper()
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	header := &tar.Header{
		Name: "sparse.db", Mode: 0o755, Size: 1, Format: tar.FormatPAX,
		PAXRecords: map[string]string{
			"BAD.sparse.map": "0,1", "BAD.sparse.numblocks": "1", "BAD.sparse.size": "1000",
		},
	}
	require.NoError(t, writer.WriteHeader(header))
	_, err := io.WriteString(writer, "x")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	patched := bytes.ReplaceAll(raw.Bytes(), []byte("BAD.sparse."), []byte("GNU.sparse."))
	path := filepath.Join(t.TempDir(), "sparse.tar.gz")
	file, err := os.Create(path)
	require.NoError(t, err)
	compressed := gzip.NewWriter(file)
	_, err = compressed.Write(patched)
	require.NoError(t, err)
	require.NoError(t, compressed.Close())
	require.NoError(t, file.Close())
	return path
}

func Test_ExtractAll_accepts_tar_when_logical_output_equals_aggregate_budget(t *testing.T) {
	formats := []string{"tar.gz", "tar.zst"}
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 4, MaxArchiveBytes: 8})
			require.NoError(t, err)
			archivePath := writeTar(t, format, []testEntry{{name: "bin/a", body: "1234"}, {name: "bin/b", body: "abcd"}})
			job := Job{ArchivePath: archivePath, Archive: format, Binaries: []Binary{
				{Source: "bin/a", Name: "a"}, {Source: "bin/b", Name: "b"},
			}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.NoError(t, err)
			require.Len(t, results, 2)
		})
	}
}

func Test_ExtractAll_rejects_zstd_frame_above_decoder_window_limit(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
	require.NoError(t, err)
	archivePath := writeTar(t, "tar.zst", []testEntry{{name: "bin/tool", body: "tool"}})
	contents, err := os.ReadFile(archivePath)
	require.NoError(t, err)
	require.Greater(t, len(contents), 6)
	if contents[4]&0x20 != 0 {
		contents[4] &^= 0x20
		contents = append(contents[:5], append([]byte{0x88}, contents[5:]...)...)
	} else {
		contents[5] = 0x88
	}
	require.NoError(t, os.WriteFile(archivePath, contents, 0o600))
	job := Job{ArchivePath: archivePath, Archive: "tar.zst", Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, zstd.ErrWindowSizeExceeded)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}

func Test_extractTar_cancellation_interrupts_implicit_entry_drain(t *testing.T) {
	// Given
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	body := strings.Repeat("x", 1<<20)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "docs/large", Mode: 0o644, Size: int64(len(body))}))
	_, err := io.WriteString(writer, body)
	require.NoError(t, err)
	require.NoError(t, writer.WriteHeader(&tar.Header{Name: "bin/tool", Mode: 0o755, Size: 4}))
	_, err = io.WriteString(writer, "tool")
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelAfterReader{reader: bytes.NewReader(archive.Bytes()), cancel: cancel, cancelAt: 513}
	tempPath := filepath.Join(t.TempDir(), "tool")
	extractor := &Extractor{maxBinaryBytes: 8, maxArchiveBytes: 2 << 20}
	plan := jobPlan{
		job:      Job{Archive: "tar.gz"},
		binaries: []binaryPlan{{source: "bin/tool", tempPath: tempPath}},
		bySource: map[string]binaryPlan{"bin/tool": {source: "bin/tool", tempPath: tempPath}},
	}

	// When
	err = extractor.extractTar(ctx, reader, plan)

	// Then
	require.ErrorIs(t, err, context.Canceled)
	require.Less(t, reader.read, int64(len(body)/2))
}

type cancelAfterReader struct {
	reader   io.Reader
	cancel   context.CancelFunc
	cancelAt int64
	read     int64
}

func (r *cancelAfterReader) Read(buffer []byte) (int, error) {
	if len(buffer) > 4<<10 {
		buffer = buffer[:4<<10]
	}
	read, err := r.reader.Read(buffer)
	r.read += int64(read)
	if r.read >= r.cancelAt {
		r.cancel()
	}
	return read, err
}

func readDirNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}
