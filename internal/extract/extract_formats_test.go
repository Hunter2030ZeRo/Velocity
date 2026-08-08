package extract

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_materializes_each_registry_format(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	jobs := []Job{
		{ArchivePath: writeZip(t, []testEntry{{name: "zip/tool", body: "zip"}}), Archive: "zip", Binaries: []Binary{{Source: "zip/tool", Name: "zip-tool"}}},
		{ArchivePath: writeTar(t, "tar.gz", []testEntry{{name: "root/gzip-tool", body: "gzip"}}), Archive: "tar.gz", StripComponents: 1, Binaries: []Binary{{Source: "gzip-tool", Name: "gzip-tool"}}},
		{ArchivePath: writeTar(t, "tar.zst", []testEntry{{name: "zstd-tool", body: "zstd"}}), Archive: "tar.zst", Binaries: []Binary{{Source: "zstd-tool", Name: "zstd-tool"}}},
		{ArchivePath: writeRaw(t, "raw"), SourceURL: "https://example.test/releases/original", Archive: "raw", Binaries: []Binary{{Source: "original", Name: "renamed-raw"}}},
	}
	extractor, err := New(Config{Concurrency: 3, MaxBinaryBytes: 32})
	require.NoError(t, err)

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, jobs)

	// Then
	require.NoError(t, err)
	require.Len(t, results, 4)
	for name, want := range map[string]string{"zip-tool": "zip", "gzip-tool": "gzip", "zstd-tool": "zstd", "renamed-raw": "raw"} {
		body, readErr := os.ReadFile(filepath.Join(stageDir, name))
		require.NoError(t, readErr)
		require.Equal(t, want, string(body))
		info, statErr := os.Stat(filepath.Join(stageDir, name))
		require.NoError(t, statErr)
		require.NotZero(t, info.Mode()&0o111)
	}
}

func Test_ExtractAll_rejects_raw_source_that_differs_from_URL_basename(t *testing.T) {
	// Given
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
	require.NoError(t, err)
	job := Job{ArchivePath: writeRaw(t, "raw"), SourceURL: "https://example.test/tool", Archive: "raw", Binaries: []Binary{{Source: "other", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), t.TempDir(), []Job{job})

	// Then
	require.Error(t, err)
	require.Nil(t, results)
}
