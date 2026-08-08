package extract

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_rejects_xz_before_opening_archive(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 8, MaxArchiveBytes: 8})
	require.NoError(t, err)
	job := Job{
		ArchivePath: filepath.Join(t.TempDir(), "does-not-exist.tar.xz"),
		Archive:     "tar.xz",
		Binaries:    []Binary{{Source: "bin/tool", Name: "tool"}},
	}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.ErrorIs(t, err, ErrUnsupportedArchive)
	require.Nil(t, results)
	require.Empty(t, readDirNames(t, stageDir))
}
