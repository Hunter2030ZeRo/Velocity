package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetcher_FetchAll_reuses_duplicate_digests(t *testing.T) {
	// Given
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeResponse(t, w, "same")
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})
	artifact := Artifact{URL: server.URL, SHA256: sha("same")}

	// When
	duplicate := artifact
	duplicate.SHA256 = strings.ToUpper(artifact.SHA256)
	paths, err := fetcher.FetchAll(context.Background(), []Artifact{artifact, duplicate})

	// Then
	mustNoError(t, err)
	if len(paths) != 1 {
		t.Fatalf("cached paths = %d, want 1", len(paths))
	}
	if _, ok := paths[artifact.SHA256]; !ok {
		t.Fatalf("missing lowercase digest key %q", artifact.SHA256)
	}
	if calls.Load() != 1 {
		t.Fatalf("server calls = %d, want 1", calls.Load())
	}
}

func TestFetcher_Fetch_rejects_malformed_sha256(t *testing.T) {
	// Given
	fetcher := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})

	// When
	_, err := fetcher.Fetch(context.Background(), Artifact{URL: "http://127.0.0.1", SHA256: "not-a-digest"})

	// Then
	if !errors.Is(err, ErrInvalidArtifact) {
		t.Fatalf("error = %v, want ErrInvalidArtifact", err)
	}
}
