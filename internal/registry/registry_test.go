package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestClient_FetchIndex_returns_verified_metadata_when_registry_is_valid(t *testing.T) {
	// Given
	indexBody := []byte("generated index")
	digest := sha256.Sum256(indexBody)
	server := registryServer(t, `{"format":1,"commit":"`+strings.Repeat("a", 40)+`","index":"registry.index","sha256":"`+hex.EncodeToString(digest[:])+`"}`, indexBody)
	client := newTestClient(t, server, t.TempDir())

	// When
	index, err := client.FetchIndex(t.Context())
	// Then
	if err != nil {
		t.Fatalf("FetchIndex() error = %v", err)
	}
	cachedIndex, readErr := os.ReadFile(index.Path)
	if readErr != nil {
		t.Fatalf("ReadFile(%q) error = %v", index.Path, readErr)
	}
	if string(cachedIndex) != string(indexBody) {
		t.Errorf("cached index = %q, want %q", cachedIndex, indexBody)
	}
	if index.Commit != strings.Repeat("a", 40) {
		t.Errorf("Index.Commit = %q, want generated commit", index.Commit)
	}
	if index.SHA256 != hex.EncodeToString(digest[:]) {
		t.Errorf("Index.SHA256 = %q, want index digest", index.SHA256)
	}
}

func TestClient_FetchIndex_rejects_unknown_metadata_fields(t *testing.T) {
	// Given
	server := registryServer(t, `{"format":1,"commit":"`+strings.Repeat("a", 40)+`","index":"registry.index","sha256":"`+strings.Repeat("b", 64)+`","surprise":true}`, nil)
	client := newTestClient(t, server, t.TempDir())

	// When
	_, err := client.FetchIndex(t.Context())

	// Then
	if err == nil {
		t.Fatal("FetchIndex() error = nil, want unknown field rejection")
	}
}

func TestClient_FetchIndex_rejects_invalid_metadata_or_path(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unsupported format", body: `{"format":2,"commit":"` + strings.Repeat("a", 40) + `","index":"registry.index","sha256":"` + strings.Repeat("b", 64) + `"}`},
		{name: "path traversal", body: `{"format":1,"commit":"` + strings.Repeat("a", 40) + `","index":"../registry.index","sha256":"` + strings.Repeat("b", 64) + `"}`},
		{name: "invalid digest", body: `{"format":1,"commit":"` + strings.Repeat("a", 40) + `","index":"registry.index","sha256":"short"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			server := registryServer(t, tt.body, nil)
			client := newTestClient(t, server, t.TempDir())

			// When
			_, err := client.FetchIndex(t.Context())

			// Then
			if err == nil {
				t.Fatal("FetchIndex() error = nil, want invalid metadata rejection")
			}
		})
	}
}

func TestClient_FetchIndex_rejects_oversized_metadata(t *testing.T) {
	// Given
	server := registryServer(t, `{"format":1,"commit":"`+strings.Repeat("a", 40)+`","index":"registry.index","sha256":"`+strings.Repeat("b", 64)+`"}`, nil)
	client, err := New(Config{
		MetadataURL:      server.URL + "/registry.json",
		CacheDir:         t.TempDir(),
		Client:           server.Client(),
		AllowHTTP:        true,
		MaxMetadataBytes: 1,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// When
	_, err = client.FetchIndex(t.Context())

	// Then
	if err == nil {
		t.Fatal("FetchIndex() error = nil, want metadata size rejection")
	}
}

func TestClient_FetchIndex_does_not_commit_index_when_digest_mismatches(t *testing.T) {
	// Given
	cacheDir := t.TempDir()
	server := registryServer(t, `{"format":1,"commit":"`+strings.Repeat("a", 40)+`","index":"registry.index","sha256":"`+strings.Repeat("f", 64)+`"}`, []byte("different index"))
	client := newTestClient(t, server, cacheDir)

	// When
	_, err := client.FetchIndex(t.Context())

	// Then
	if err == nil {
		t.Fatal("FetchIndex() error = nil, want digest mismatch")
	}
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil {
		t.Fatalf("ReadDir(%q) error = %v", cacheDir, readErr)
	}
	if len(entries) != 0 {
		t.Errorf("cache entries = %d, want no committed index", len(entries))
	}
}

func TestClient_FetchIndex_returns_context_cancellation(t *testing.T) {
	// Given
	started := make(chan struct{}, 1)
	aborted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		<-request.Context().Done()
		aborted <- struct{}{}
	}))
	t.Cleanup(server.Close)
	client := newTestClient(t, server, t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)

	// When
	go func() {
		_, err := client.FetchIndex(ctx)
		result <- err
	}()
	<-started
	cancel()
	err := <-result

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchIndex() error = %v, want context cancellation", err)
	}
	<-aborted
}

func TestNew_rejects_insecure_or_unsafe_metadata_URL(t *testing.T) {
	tests := []string{
		"http://registry.example/registry.json",
		"https://user:pass@registry.example/registry.json",
		"https://registry.example/registry.json#fragment",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			// Given
			config := Config{MetadataURL: rawURL, CacheDir: t.TempDir()}

			// When
			_, err := New(config)

			// Then
			if err == nil {
				t.Fatal("New() error = nil, want unsafe metadata URL rejection")
			}
		})
	}
}

func TestNew_uses_generated_branch_metadata_by_default(t *testing.T) {
	// Given
	config := Config{CacheDir: t.TempDir()}

	// When
	client, err := New(config)
	// Then
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if client.metadataURL.String() != DefaultMetadataURL {
		t.Errorf("metadata URL = %q, want %q", client.metadataURL, DefaultMetadataURL)
	}
}

func registryServer(t *testing.T, metadata string, index []byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/registry.json":
			writeTestResponse(t, writer, []byte(metadata))
		case "/registry.index":
			writeTestResponse(t, writer, index)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func writeTestResponse(t *testing.T, writer http.ResponseWriter, body []byte) {
	t.Helper()
	if _, err := writer.Write(body); err != nil {
		t.Errorf("Write() error = %v", err)
	}
}

func newTestClient(t *testing.T, server *httptest.Server, cacheDir string) *Client {
	t.Helper()
	client, err := New(Config{
		MetadataURL: server.URL + "/registry.json",
		CacheDir:    cacheDir,
		Client:      server.Client(),
		AllowHTTP:   true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}
