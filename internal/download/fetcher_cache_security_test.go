package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestFetcher_Fetch_removes_partial_file_when_checksum_mismatches(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponse(t, w, "wrong")
	}))
	defer server.Close()
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})

	// When
	_, err := fetcher.Fetch(context.Background(), Artifact{URL: server.URL, SHA256: sha("expected")})

	// Then
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("error = %v, want ErrChecksum", err)
	}
	entries, readErr := os.ReadDir(cacheDir)
	mustNoError(t, readErr)
	if len(entries) != 0 {
		t.Fatalf("cache has %d entries, want none", len(entries))
	}
}

func TestFetcher_Fetch_reuses_verified_cache(t *testing.T) {
	// Given
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, w, "cached")
	}))
	cacheDir := privateCacheDir(t)
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})
	artifact := Artifact{URL: server.URL, SHA256: sha("cached")}

	// When
	first, err := fetcher.Fetch(context.Background(), artifact)
	mustNoError(t, err)
	server.Close()
	second, err := fetcher.Fetch(context.Background(), artifact)

	// Then
	mustNoError(t, err)
	if first != second {
		t.Fatalf("cache paths differ: %q and %q", first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
}

func TestFetcher_Fetch_rejects_insecure_urls_and_https_downgrades(t *testing.T) {
	// Given
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponse(t, w, "plain")
	}))
	defer plain.Close()
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusFound)
	}))
	defer secure.Close()
	strict := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024})
	downgrade := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true, Client: secure.Client()})

	// When
	_, plainErr := strict.Fetch(context.Background(), Artifact{URL: plain.URL, SHA256: sha("plain")})
	_, redirectErr := downgrade.Fetch(context.Background(), Artifact{URL: secure.URL, SHA256: sha("plain")})

	// Then
	if !errors.Is(plainErr, ErrInsecureURL) {
		t.Fatalf("plain fetch error = %v, want ErrInsecureURL", plainErr)
	}
	if !errors.Is(redirectErr, ErrInsecureURL) {
		t.Fatalf("redirect fetch error = %v, want ErrInsecureURL", redirectErr)
	}
}

func TestFetcher_Fetch_discards_oversize_download(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponse(t, w, "toolarge")
	}))
	defer server.Close()
	cacheDir := privateCacheDir(t)
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 3, AllowHTTP: true})

	// When
	_, err := fetcher.Fetch(context.Background(), Artifact{URL: server.URL, SHA256: sha("toolarge")})

	// Then
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
	entries, readErr := os.ReadDir(cacheDir)
	mustNoError(t, readErr)
	if len(entries) != 0 {
		t.Fatalf("cache has %d entries, want none", len(entries))
	}
}

func TestFetcher_Fetch_does_not_cache_non_success_status(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cacheDir := privateCacheDir(t)
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})

	// When
	_, err := fetcher.Fetch(context.Background(), Artifact{URL: server.URL, SHA256: sha("unavailable")})

	// Then
	if err == nil {
		t.Fatal("expected HTTP status error")
	}
	entries, readErr := os.ReadDir(cacheDir)
	mustNoError(t, readErr)
	if len(entries) != 0 {
		t.Fatalf("cache has %d entries, want none", len(entries))
	}
}

func TestFetcher_Fetch_replaces_corrupt_cache_entry(t *testing.T) {
	// Given
	content := "verified"
	artifact := Artifact{SHA256: sha(content)}
	cacheDir := privateCacheDir(t)
	mustNoError(t, os.Mkdir(cacheDir, 0o700))
	artifactPath := cacheDir + "/" + artifact.SHA256
	mustNoError(t, os.WriteFile(artifactPath, []byte("corrupt"), 0o600))
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, w, content)
	}))
	defer server.Close()
	artifact.URL = server.URL
	fetcher := mustNew(t, Config{CacheDir: cacheDir, Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})

	// When
	path, err := fetcher.Fetch(context.Background(), artifact)

	// Then
	mustNoError(t, err)
	stored, readErr := os.ReadFile(path)
	mustNoError(t, readErr)
	if string(stored) != content {
		t.Fatalf("cached content = %q, want %q", stored, content)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
}
