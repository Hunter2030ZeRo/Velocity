package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetcher_FetchAll_rejects_artifact_count_before_downloading(t *testing.T) {
	// Given
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, w, "one")
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 8,
		MaxTotalBytes: 8, MaxArtifacts: 1, AllowHTTP: true,
	})
	artifacts := []Artifact{
		{URL: server.URL + "/one", SHA256: sha("one")},
		{URL: server.URL + "/two", SHA256: sha("two")},
	}

	// When
	_, err := fetcher.FetchAll(context.Background(), artifacts)

	// Then
	if !errors.Is(err, ErrArtifactLimit) {
		t.Fatalf("error = %v, want ErrArtifactLimit", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("server calls = %d, want 0", calls.Load())
	}
}

func TestFetcher_FetchAll_rolls_back_new_entries_when_stream_budget_is_exceeded(t *testing.T) {
	// Given
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	kept := "kept"
	keptPath := filepath.Join(cacheDir, sha(kept))
	mustNoError(t, os.WriteFile(keptPath, []byte(kept), 0o600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, r.URL.Path[1:])
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{
		CacheDir: cacheDir, Concurrency: 1, MaxBytes: 8,
		MaxTotalBytes: 3, MaxArtifacts: 3, AllowHTTP: true,
	})
	artifacts := []Artifact{
		{URL: server.URL + "/" + kept, SHA256: sha(kept)},
		{URL: server.URL + "/new", SHA256: sha("new")},
		{URL: server.URL + "/more", SHA256: sha("more")},
	}

	// When
	_, err := fetcher.FetchAll(context.Background(), artifacts)

	// Then
	if !errors.Is(err, ErrFetchLimit) {
		t.Fatalf("error = %v, want ErrFetchLimit", err)
	}
	stored, readErr := os.ReadFile(keptPath)
	mustNoError(t, readErr)
	if string(stored) != kept {
		t.Fatalf("pre-existing cache content = %q, want %q", stored, kept)
	}
	if _, statErr := os.Stat(filepath.Join(cacheDir, sha("new"))); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new cache entry remains after rollback: %v", statErr)
	}
	entries, readErr := os.ReadDir(cacheDir)
	mustNoError(t, readErr)
	if len(entries) != 1 {
		t.Fatalf("cache entries = %d, want only pre-existing entry", len(entries))
	}
}

func TestFetcher_FetchAll_shares_stream_budget_across_concurrent_workers(t *testing.T) {
	// Given
	cacheDir := privateCacheDir(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponse(t, w, r.URL.Path[1:])
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{
		CacheDir: cacheDir, Concurrency: 2, MaxBytes: 8,
		MaxTotalBytes: 4, MaxArtifacts: 2, AllowHTTP: true,
	})
	artifacts := []Artifact{
		{URL: server.URL + "/aaaa", SHA256: sha("aaaa")},
		{URL: server.URL + "/bbbb", SHA256: sha("bbbb")},
	}

	// When
	_, err := fetcher.FetchAll(context.Background(), artifacts)

	// Then
	if !errors.Is(err, ErrFetchLimit) {
		t.Fatalf("error = %v, want ErrFetchLimit", err)
	}
	entries, readErr := os.ReadDir(cacheDir)
	mustNoError(t, readErr)
	if len(entries) != 0 {
		t.Fatalf("cache has %d entries after concurrent rollback, want none", len(entries))
	}
}

func TestFetcher_Fetch_returns_cancellation_before_parsing(t *testing.T) {
	// Given
	fetcher := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 8})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err := fetcher.Fetch(ctx, Artifact{URL: ":", SHA256: "invalid"})

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestFetcher_Fetch_joins_owned_work_after_cancellation(t *testing.T) {
	// Given
	started := make(chan struct{})
	release := make(chan struct{})
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		<-release
		return nil, request.Context().Err()
	})
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 8,
		Client: &http.Client{Transport: transport}, AllowHTTP: true,
	})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := fetcher.Fetch(ctx, Artifact{URL: "http://artifact.test/tool", SHA256: sha("tool")})
		result <- err
	}()
	<-started

	// When
	cancel()
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-result:
		close(release)
		t.Fatalf("Fetch returned before owned work stopped: %v", err)
	case <-timer.C:
		close(release)
	}
	err := <-result

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestFetcher_Fetch_rejects_symlink_cache_entry(t *testing.T) {
	// Given
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	target := filepath.Join(t.TempDir(), "target")
	mustNoError(t, os.WriteFile(target, []byte("cached"), 0o600))
	mustNoError(t, os.Symlink(target, filepath.Join(cacheDir, sha("cached"))))
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

func TestContextReader_stops_before_next_cache_hash_read(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader := contextReader{ctx: ctx, source: io.LimitReader(zeroReader{}, 1)}

	// When
	_, err := reader.Read(make([]byte, 1))

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
