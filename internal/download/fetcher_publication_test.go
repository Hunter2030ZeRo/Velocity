package download

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFetcher_FetchAll_failed_peer_does_not_remove_successful_batch_entry(t *testing.T) {
	// Given
	shared := "shared"
	successStarted := make(chan struct{})
	peerStarted := make(chan struct{})
	releaseSuccess := make(chan struct{})
	successPublished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("batch") {
		case "success":
			close(successStarted)
			<-releaseSuccess
			writeResponse(t, w, shared)
		case "peer":
			close(peerStarted)
			<-successPublished
			writeResponse(t, w, shared)
		case "fail":
			writeResponse(t, w, "wrong")
		default:
			t.Errorf("unexpected request %q", r.URL.String())
		}
	}))
	defer server.Close()
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 16,
		MaxTotalBytes: 32, MaxArtifacts: 2, AllowHTTP: true,
	})
	type fetchAllResult struct {
		paths map[string]string
		err   error
	}
	successResult := make(chan fetchAllResult, 1)
	peerResult := make(chan fetchAllResult, 1)
	go func() {
		paths, err := fetcher.FetchAll(context.Background(), []Artifact{{
			URL: server.URL + "/shared?batch=success", SHA256: sha(shared),
		}})
		successResult <- fetchAllResult{paths: paths, err: err}
	}()
	<-successStarted
	go func() {
		paths, err := fetcher.FetchAll(context.Background(), []Artifact{
			{URL: server.URL + "/shared?batch=peer", SHA256: sha(shared)},
			{URL: server.URL + "/fail?batch=fail", SHA256: sha("expected")},
		})
		peerResult <- fetchAllResult{paths: paths, err: err}
	}()
	<-peerStarted

	// When
	close(releaseSuccess)
	succeeded := <-successResult
	close(successPublished)
	failed := <-peerResult

	// Then
	mustNoError(t, succeeded.err)
	if !errors.Is(failed.err, ErrChecksum) {
		t.Fatalf("peer error = %v, want ErrChecksum", failed.err)
	}
	path := succeeded.paths[sha(shared)]
	info, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("successful cache entry: %v", statErr)
		return
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("successful cache entry mode = %v, want regular file", info.Mode())
	}
	if validationErr := validateCacheEntry(path, info); validationErr != nil {
		t.Fatalf("successful cache entry is unsafe: %v", validationErr)
	}
	content, readErr := os.ReadFile(path)
	mustNoError(t, readErr)
	if sha(string(content)) != sha(shared) {
		t.Fatalf("successful cache checksum = %s, want %s", sha(string(content)), sha(shared))
	}
}
