package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFetcher_FetchAll_charges_repeated_cached_digest_once(t *testing.T) {
	// Given
	content := "cached"
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	path := filepath.Join(cacheDir, sha(content))
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	fetcher := mustNew(t, Config{
		CacheDir: cacheDir, Concurrency: 2, MaxBytes: 16,
		MaxTotalBytes: int64(len(content)), MaxArtifacts: 2,
	})
	artifact := Artifact{URL: "https://artifact.test/tool", SHA256: sha(content)}

	// When
	paths, err := fetcher.FetchAll(context.Background(), []Artifact{artifact, artifact})

	// Then
	mustNoError(t, err)
	if paths[sha(content)] != path {
		t.Fatalf("cached path = %q, want %q", paths[sha(content)], path)
	}
}

func TestFetcher_FetchAll_rejects_distinct_cached_artifacts_over_total_budget_without_deleting(t *testing.T) {
	// Given
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	contents := []string{"one", "two"}
	artifacts := make([]Artifact, 0, len(contents))
	for _, content := range contents {
		mustNoError(t, os.WriteFile(filepath.Join(cacheDir, sha(content)), []byte(content), 0o600))
		artifacts = append(artifacts, Artifact{
			URL:    "https://artifact.test/" + content,
			SHA256: sha(content),
		})
	}
	fetcher := mustNew(t, Config{
		CacheDir: cacheDir, Concurrency: 2, MaxBytes: 16,
		MaxTotalBytes: 5, MaxArtifacts: 2,
	})

	// When
	_, err := fetcher.FetchAll(context.Background(), artifacts)

	// Then
	if !errors.Is(err, ErrFetchLimit) {
		t.Fatalf("error = %v, want ErrFetchLimit", err)
	}
	for _, content := range contents {
		stored, readErr := os.ReadFile(filepath.Join(cacheDir, sha(content)))
		mustNoError(t, readErr)
		if string(stored) != content {
			t.Fatalf("cached content = %q, want %q", stored, content)
		}
	}
}
