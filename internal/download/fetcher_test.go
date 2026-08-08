package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestFetcher_FetchAll_downloads_with_bounded_parallelism(t *testing.T) {
	// Given
	contents := map[string]string{"/one": "one", "/two": "two", "/three": "three", "/four": "four"}
	started := make(chan struct{}, len(contents))
	release := make(chan struct{})
	var inFlight, peak atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
		}
		started <- struct{}{}
		<-release
		writeResponse(t, w, contents[r.URL.Path])
	}))
	defer server.Close()

	fetcher := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 2, MaxBytes: 1024, AllowHTTP: true})
	artifacts := make([]Artifact, 0, len(contents))
	for path, content := range contents {
		artifacts = append(artifacts, Artifact{URL: server.URL + path, SHA256: sha(content)})
	}
	result := make(chan struct {
		paths map[string]string
		err   error
	}, 1)

	// When
	go func() {
		paths, fetchErr := fetcher.FetchAll(context.Background(), artifacts)
		result <- struct {
			paths map[string]string
			err   error
		}{paths: paths, err: fetchErr}
	}()
	<-started
	<-started
	close(release)
	got := <-result

	// Then
	mustNoError(t, got.err)
	if len(got.paths) != len(contents) {
		t.Fatalf("got %d cached paths, want %d", len(got.paths), len(contents))
	}
	if peak.Load() <= 1 || peak.Load() > 2 {
		t.Fatalf("peak in-flight requests = %d, want 2", peak.Load())
	}
	for _, content := range contents {
		path, ok := got.paths[sha(content)]
		if !ok {
			t.Fatalf("missing cache path for %s", sha(content))
		}
		stored, readErr := os.ReadFile(path)
		mustNoError(t, readErr)
		if string(stored) != content {
			t.Fatalf("cached content = %q, want %q", stored, content)
		}
	}
}

func TestFetcher_Fetch_stops_request_when_context_is_cancelled(t *testing.T) {
	// Given
	started := make(chan struct{}, 1)
	aborted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		aborted <- struct{}{}
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	// When
	go func() {
		_, fetchErr := fetcher.Fetch(ctx, Artifact{URL: server.URL, SHA256: sha("content")})
		result <- fetchErr
	}()
	<-started
	cancel()
	fetchErr := <-result

	// Then
	if !errors.Is(fetchErr, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", fetchErr)
	}
	<-aborted
}
