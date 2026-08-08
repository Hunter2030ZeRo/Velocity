package extract

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_ExtractAll_rejects_unsafe_paths_in_unmapped_entries(t *testing.T) {
	tests := map[string]struct {
		archive string
		path    string
	}{
		"zip traversal": {archive: "zip", path: "../escape"},
		"zip absolute":  {archive: "zip", path: "/escape"},
		"tar traversal": {archive: "tar.gz", path: "safe/../../escape"},
		"tar absolute":  {archive: "tar.gz", path: "/escape"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			entries := []testEntry{{name: "tool", body: "ok"}, {name: test.path, body: "unsafe"}}
			archivePath := writeZip(t, entries)
			if test.archive != "zip" {
				archivePath = writeTar(t, test.archive, entries)
			}
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
			require.NoError(t, err)

			// When
			results, err := extractor.ExtractAll(context.Background(), t.TempDir(), []Job{{ArchivePath: archivePath, Archive: test.archive, Binaries: []Binary{{Source: "tool", Name: "tool"}}}})

			// Then
			require.Error(t, err)
			require.Nil(t, results)
		})
	}
}

func Test_ExtractAll_rejects_archive_links(t *testing.T) {
	tests := map[string]struct {
		archive string
		entry   testEntry
	}{
		"zip symlink":  {archive: "zip", entry: testEntry{name: "link", body: "target", mode: int64(os.ModeSymlink | 0o777)}},
		"tar symlink":  {archive: "tar.gz", entry: testEntry{name: "link", typeflag: tar.TypeSymlink}},
		"tar hardlink": {archive: "tar.gz", entry: testEntry{name: "link", typeflag: tar.TypeLink}},
		"tar device":   {archive: "tar.gz", entry: testEntry{name: "device", typeflag: tar.TypeChar}},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			entries := []testEntry{{name: "tool", body: "ok"}, test.entry}
			archivePath := writeZip(t, entries)
			if test.archive != "zip" {
				archivePath = writeTar(t, test.archive, entries)
			}
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
			require.NoError(t, err)

			// When
			results, err := extractor.ExtractAll(context.Background(), t.TempDir(), []Job{{ArchivePath: archivePath, Archive: test.archive, Binaries: []Binary{{Source: "tool", Name: "tool"}}}})

			// Then
			require.Error(t, err)
			require.Nil(t, results)
		})
	}
}

func Test_ExtractAll_rejects_invalid_or_colliding_mappings_before_writing(t *testing.T) {
	tests := map[string][]Job{
		"source traversal": {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "../tool", Name: "tool"}}}},
		"source absolute":  {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "/tool", Name: "tool"}}}},
		"name directory":   {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "tool", Name: "bin/tool"}}}},
		"name parent":      {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "tool", Name: ".."}}}},
		"name root":        {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "tool", Name: "/"}}}},
		"name backslash":   {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "tool", Name: `bin\tool`}}}},
		"duplicate name":   {{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "a", Name: "tool"}, {Source: "b", Name: "tool"}}}},
		"cross-job name": {
			{ArchivePath: "missing-a", Archive: "zip", Binaries: []Binary{{Source: "a", Name: "tool"}}},
			{ArchivePath: "missing-b", Archive: "zip", Binaries: []Binary{{Source: "b", Name: "tool"}}},
		},
	}
	for name, jobs := range tests {
		t.Run(name, func(t *testing.T) {
			// Given
			stageDir := t.TempDir()
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
			require.NoError(t, err)

			// When
			results, err := extractor.ExtractAll(context.Background(), stageDir, jobs)

			// Then
			require.Error(t, err)
			require.Nil(t, results)
			entries, readErr := os.ReadDir(stageDir)
			require.NoError(t, readErr)
			require.Empty(t, entries)
		})
	}
}

func Test_ExtractAll_rejects_existing_destination_collision(t *testing.T) {
	// Given
	stageDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stageDir, "tool"), []byte("existing"), 0o755))
	extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
	require.NoError(t, err)
	job := Job{ArchivePath: writeRaw(t, "new"), SourceURL: "https://example.test/tool", Archive: "raw", Binaries: []Binary{{Source: "tool", Name: "tool"}}}

	// When
	results, err := extractor.ExtractAll(context.Background(), stageDir, []Job{job})

	// Then
	require.Error(t, err)
	require.Nil(t, results)
	body, readErr := os.ReadFile(filepath.Join(stageDir, "tool"))
	require.NoError(t, readErr)
	require.Equal(t, "existing", string(body))
}

func Test_ExtractAll_rejects_non_basename_exposed_names_at_boundary(t *testing.T) {
	for _, name := range []string{"..", "/", `bin\tool`} {
		t.Run(name, func(t *testing.T) {
			// Given
			extractor, err := New(Config{Concurrency: 1, MaxBinaryBytes: 32})
			require.NoError(t, err)
			job := Job{ArchivePath: "missing", Archive: "zip", Binaries: []Binary{{Source: "tool", Name: name}}}

			// When
			results, err := extractor.ExtractAll(context.Background(), t.TempDir(), []Job{job})

			// Then
			require.ErrorIs(t, err, ErrInvalidJob)
			require.Nil(t, results)
		})
	}
}
