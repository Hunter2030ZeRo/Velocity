package download

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFetcher_FetchAll_reports_byte_and_artifact_progress(t *testing.T) {
	// Given
	content := strings.Repeat("velocity", 16*1024)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
		writeResponse(t, writer, content)
	}))
	defer server.Close()

	var snapshots []Progress
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: int64(len(content) + 1), AllowHTTP: true,
		Progress: func(progress Progress) { snapshots = append(snapshots, progress) },
	})

	// When
	_, err := fetcher.FetchAll(context.Background(), []Artifact{{URL: server.URL, SHA256: sha(content)}})

	// Then
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(snapshots), 3)
	require.Equal(t, Progress{BatchID: 1, TotalArtifacts: 1}, snapshots[0])
	require.Contains(t, snapshots, Progress{
		BatchID:         1,
		DownloadedBytes: int64(len(content)),
		TotalBytes:      int64(len(content)),
		TotalArtifacts:  1,
		Percent:         99,
	})
	require.Equal(t, Progress{
		BatchID:            1,
		DownloadedBytes:    int64(len(content)),
		TotalBytes:         int64(len(content)),
		CompletedArtifacts: 1,
		TotalArtifacts:     1,
		Percent:            100,
	}, snapshots[len(snapshots)-1])
}

func TestFetcher_FetchAll_reports_cache_hit_without_downloaded_bytes(t *testing.T) {
	// Given
	const content = "cached artifact"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(t, writer, content)
	}))
	defer server.Close()

	var snapshots []Progress
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true,
		Progress: func(progress Progress) { snapshots = append(snapshots, progress) },
	})
	artifact := Artifact{URL: server.URL, SHA256: sha(content)}
	_, err := fetcher.FetchAll(context.Background(), []Artifact{artifact})
	require.NoError(t, err)
	snapshots = nil

	// When
	_, err = fetcher.FetchAll(context.Background(), []Artifact{artifact})

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, snapshots)
	require.Equal(t, Progress{
		BatchID:            2,
		CompletedArtifacts: 1,
		TotalArtifacts:     1,
		Percent:            100,
	}, snapshots[len(snapshots)-1])
}

func TestFetcher_FetchAll_serializes_progress_from_concurrent_downloads(t *testing.T) {
	// Given
	contents := map[string]string{
		"/one": strings.Repeat("one", 128*1024),
		"/two": strings.Repeat("two", 128*1024),
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		content := contents[request.URL.Path]
		writer.Header().Set("Content-Length", strconv.Itoa(len(content)))
		writeResponse(t, writer, content)
	}))
	defer server.Close()

	var inside, overlap atomic.Bool
	var snapshots []Progress
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 2, MaxBytes: 1 << 20, AllowHTTP: true,
		Progress: func(progress Progress) {
			if !inside.CompareAndSwap(false, true) {
				overlap.Store(true)
				return
			}
			defer inside.Store(false)
			snapshots = append(snapshots, progress)
			for range 10 {
				runtime.Gosched()
			}
		},
	})
	artifacts := make([]Artifact, 0, len(contents))
	for path, content := range contents {
		artifacts = append(artifacts, Artifact{URL: server.URL + path, SHA256: sha(content)})
	}

	// When
	_, err := fetcher.FetchAll(context.Background(), artifacts)

	// Then
	require.NoError(t, err)
	require.False(t, overlap.Load())
	require.NotEmpty(t, snapshots)
	for index := 1; index < len(snapshots); index++ {
		require.GreaterOrEqual(t, snapshots[index].DownloadedBytes, snapshots[index-1].DownloadedBytes)
		require.GreaterOrEqual(t, snapshots[index].CompletedArtifacts, snapshots[index-1].CompletedArtifacts)
		require.GreaterOrEqual(t, snapshots[index].Percent, snapshots[index-1].Percent)
	}
	last := snapshots[len(snapshots)-1]
	require.Equal(t, 2, last.CompletedArtifacts)
	require.Equal(t, 2, last.TotalArtifacts)
	require.Equal(t, 100, last.Percent)
}

func TestFetcher_FetchAll_serializes_and_identifies_concurrent_batches(t *testing.T) {
	// Given
	contents := map[string]string{"/one": "first batch", "/two": "second batch"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writeResponse(t, writer, contents[request.URL.Path])
	}))
	defer server.Close()

	var inside, overlap atomic.Bool
	var snapshotsMu sync.Mutex
	snapshots := make(map[BatchID][]Progress)
	fetcher := mustNew(t, Config{
		CacheDir: privateCacheDir(t), Concurrency: 2, MaxBytes: 1024, AllowHTTP: true,
		Progress: func(progress Progress) {
			if !inside.CompareAndSwap(false, true) {
				overlap.Store(true)
			}
			defer inside.Store(false)
			snapshotsMu.Lock()
			snapshots[progress.BatchID] = append(snapshots[progress.BatchID], progress)
			snapshotsMu.Unlock()
			runtime.Gosched()
		},
	})
	start := make(chan struct{})
	errors := make(chan error, len(contents))
	var workers sync.WaitGroup
	for path, content := range contents {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := fetcher.FetchAll(context.Background(), []Artifact{{
				URL: server.URL + path, SHA256: sha(content),
			}})
			errors <- err
		}()
	}

	// When
	close(start)
	workers.Wait()
	close(errors)

	// Then
	for err := range errors {
		require.NoError(t, err)
	}
	require.False(t, overlap.Load())
	require.Len(t, snapshots, 2)
	for _, batch := range snapshots {
		require.NotEmpty(t, batch)
		last := batch[len(batch)-1]
		require.Equal(t, 1, last.CompletedArtifacts)
		require.Equal(t, 100, last.Percent)
	}
}

func TestFetcher_FetchAll_reports_indeterminate_progress_for_unknown_length(t *testing.T) {
	// Given
	const content = "chunked artifact"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(content)),
			ContentLength: -1, Header: make(http.Header), Request: request,
		}, nil
	})}
	var snapshots []Progress
	fetcher := mustNew(t, Config{
		Client: client, CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true,
		Progress: func(progress Progress) { snapshots = append(snapshots, progress) },
	})

	// When
	_, err := fetcher.FetchAll(context.Background(), []Artifact{{
		URL: "http://artifact.test/chunked", SHA256: sha(content),
	}})

	// Then
	require.NoError(t, err)
	require.Condition(t, func() bool {
		for _, snapshot := range snapshots {
			if snapshot.Indeterminate {
				return true
			}
		}
		return false
	})
	last := snapshots[len(snapshots)-1]
	require.Equal(t, int64(len(content)), last.DownloadedBytes)
	require.Equal(t, int64(len(content)), last.TotalBytes)
	require.False(t, last.Indeterminate)
	require.Equal(t, 100, last.Percent)
}

func TestFetcher_FetchAll_reconciles_progress_when_content_length_is_understated(t *testing.T) {
	// Given
	const content = "longer than declared"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(content)),
			ContentLength: 1, Header: make(http.Header), Request: request,
		}, nil
	})}
	var snapshots []Progress
	fetcher := mustNew(t, Config{
		Client: client, CacheDir: privateCacheDir(t), Concurrency: 1, MaxBytes: 1024, AllowHTTP: true,
		Progress: func(progress Progress) { snapshots = append(snapshots, progress) },
	})

	// When
	_, err := fetcher.FetchAll(context.Background(), []Artifact{{
		URL: "http://artifact.test/mismatch", SHA256: sha(content),
	}})

	// Then
	require.NoError(t, err)
	require.Condition(t, func() bool {
		for _, snapshot := range snapshots {
			if snapshot.Indeterminate {
				return true
			}
		}
		return false
	})
	last := snapshots[len(snapshots)-1]
	require.Equal(t, last.DownloadedBytes, last.TotalBytes)
	require.Equal(t, int64(len(content)), last.TotalBytes)
	require.Equal(t, 100, last.Percent)
}
