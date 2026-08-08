//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package download

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFetcher_Fetch_rejects_insecure_cache_directory(t *testing.T) {
	// Given
	cacheDir := filepath.Join(t.TempDir(), "cache")
	mustNoError(t, os.Mkdir(cacheDir, 0o755))
	mustNoError(t, os.Chmod(cacheDir, 0o777))
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 8})

	// When
	_, err := fetcher.Fetch(context.Background(), Artifact{
		URL: "https://artifact.test/tool", SHA256: sha("cached"),
	})

	// Then
	if !errors.Is(err, ErrUnsafeCache) {
		t.Fatalf("error = %v, want ErrUnsafeCache", err)
	}
}
