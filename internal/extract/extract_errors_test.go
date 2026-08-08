package extract

import (
	"context"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_rejects_missing_and_duplicate_archive_sources(t *testing.T) {
	tests := map[string][]testEntry{
		"missing source":   {{name: "other", body: "data"}},
		"duplicate source": {{name: "tool", body: "first"}, {name: "tool", body: "second"}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
			require.NoError(t, err)
			job := Job{ArchivePath: writeZip(t, entries), Archive: "zip", Binaries: []Binary{{Source: "tool", Name: "tool"}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

			// Then
			require.Error(t, err)
			require.Nil(t, results)
			files, readErr := os.ReadDir(stageDir)
			require.NoError(t, readErr)
			require.Empty(t, files)
		})
	}
}

func Test_ExtractAll_rejects_binary_over_size_cap_and_cleans_stage(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 2, MaxBinaryBytes: 3})
	require.NoError(t, err)
	jobs := []Job{
		{ArchivePath: writeRaw(t, "ok"), SourceURL: "https://example.test/ok", Archive: "raw", Binaries: []Binary{{Source: "ok", Name: "ok"}}},
		{ArchivePath: writeRaw(t, "oversized"), SourceURL: "https://example.test/large", Archive: "raw", Binaries: []Binary{{Source: "large", Name: "large"}}},
	}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, jobs)

	// Then
	require.Error(t, err)
	require.Nil(t, results)
	files, readErr := os.ReadDir(stageDir)
	require.NoError(t, readErr)
	require.Empty(t, files)
}

func Test_ExtractAll_honors_pre_cancelled_context_without_artifacts(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
	require.NoError(t, err)
	job := Job{ArchivePath: writeRaw(t, "tool"), SourceURL: "https://example.test/tool", Archive: "raw", Binaries: []Binary{{Source: "tool", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(ctx, stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, results)
	files, readErr := os.ReadDir(stageDir)
	require.NoError(t, readErr)
	require.Empty(t, files)
}

func Test_New_rejects_invalid_bounds(t *testing.T) {
	tests := map[string]Config{
		"zero concurrency":            {Concurrency: 0, MaxBinaryBytes: 1},
		"zero byte cap":               {Concurrency: 1, MaxBinaryBytes: 0},
		"negative expanded byte cap":  {Concurrency: 1, MaxBinaryBytes: 1, MaxArchiveBytes: -1},
		"unbounded expanded byte cap": {Concurrency: 1, MaxBinaryBytes: 1, MaxArchiveBytes: math.MaxInt64},
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			// When
			extractor, err := New(config)

			// Then
			require.Error(t, err)
			require.Nil(t, extractor)
		})
	}
}
