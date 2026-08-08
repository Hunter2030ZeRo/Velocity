package extract

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_limits_expanded_tar_stream_and_preserves_valid_archives(t *testing.T) {
	formats := []string{"tar.gz", "tar.zst"}
	for _, format := range formats {
		t.Run(format+" rejects oversized unmapped entry", func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32, MaxArchiveBytes: 4 << 10})
			require.NoError(t, err)
			archivePath := writeTar(t, format, []testEntry{
				{name: "bin/tool", body: "tool"},
				{name: "docs/release-notes", body: strings.Repeat("x", 16<<10)},
			})
			job := Job{ArchivePath: archivePath, Archive: format, Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.ErrorIs(t, err, ErrArchiveTooLarge)
			require.Nil(t, results)
			files, readErr := os.ReadDir(stageDir)
			require.NoError(t, readErr)
			require.Empty(t, files)
		})

		t.Run(format+" accepts legitimate archive within expansion cap", func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32, MaxArchiveBytes: 4 << 10})
			require.NoError(t, err)
			archivePath := writeTar(t, format, []testEntry{{name: "bin/tool", body: "tool"}})
			job := Job{ArchivePath: archivePath, Archive: format, Binaries: []Binary{{Source: "bin/tool", Name: "tool"}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.NoError(t, err)
			require.Len(t, results, 1)
			body, readErr := os.ReadFile(results[0].Path)
			require.NoError(t, readErr)
			require.Equal(t, "tool", string(body))
		})
	}
}
