package registry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestConfiguredMetadataClient_rejects_redirect_at_ten_hops_after_caller_policy(t *testing.T) {
	// Given
	callerCalls := 0
	source := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		callerCalls++
		return nil
	}}
	configured := configuredMetadataClient(source, true)
	request := &http.Request{URL: &url.URL{Scheme: "http", Host: "example.test", Path: "/hop/10"}}
	via := make([]*http.Request, 10)
	for index := range via {
		via[index] = request
	}

	// When
	redirectErr := configured.CheckRedirect(request, via)

	// Then
	if !errors.Is(redirectErr, ErrInvalidMetadataURL) {
		t.Fatalf("redirect error = %v, want ErrInvalidMetadataURL", redirectErr)
	}
	if callerCalls != 1 {
		t.Fatalf("caller CheckRedirect calls = %d, want 1", callerCalls)
	}
}

func TestClient_FetchIndex_rejects_https_metadata_downgrade_before_http_request(t *testing.T) {
	// Given
	indexBody := []byte("generated index")
	digest := sha256.Sum256(indexBody)
	metadata := `{"format":1,"commit":"` + strings.Repeat("a", 40) + `","index":"registry.index","sha256":"` + hex.EncodeToString(digest[:]) + `"}`
	httpDestinationRequests := make(chan struct{}, 1)
	plain := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		httpDestinationRequests <- struct{}{}
		if request.URL.Path != "/registry.json" {
			http.NotFound(writer, request)
			return
		}
		writeTestResponse(t, writer, []byte(metadata))
	}))
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/registry.json":
			http.Redirect(writer, request, plain.URL+"/registry.json", http.StatusFound)
		case "/registry.index":
			writeTestResponse(t, writer, indexBody)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(secure.Close)

	// When
	strict, err := New(Config{MetadataURL: secure.URL + "/registry.json", CacheDir: t.TempDir(), Client: secure.Client()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, strictErr := strict.FetchIndex(t.Context())

	// Then
	if strictErr == nil || !errors.Is(strictErr, ErrInvalidMetadataURL) {
		t.Fatalf("strict FetchIndex() error = %v, want ErrInvalidMetadataURL", strictErr)
	}
	select {
	case <-httpDestinationRequests:
		t.Fatal("HTTP downgrade destination was reached before rejection")
	default:
	}

	permissive, err := New(Config{
		MetadataURL: secure.URL + "/registry.json",
		CacheDir:    t.TempDir(),
		Client:      secure.Client(),
		AllowHTTP:   true,
	})
	if err != nil {
		t.Fatalf("New() with AllowHTTP error = %v", err)
	}
	_, permissiveErr := permissive.FetchIndex(t.Context())
	if permissiveErr == nil || !errors.Is(permissiveErr, ErrInvalidMetadataURL) {
		t.Fatalf("AllowHTTP FetchIndex() error = %v, want ErrInvalidMetadataURL", permissiveErr)
	}
}

func TestClient_FetchIndex_preserves_same_scheme_redirect_and_caller_client(t *testing.T) {
	// Given
	indexBody := []byte("generated index")
	digest := sha256.Sum256(indexBody)
	metadata := `{"format":1,"commit":"` + strings.Repeat("a", 40) + `","index":"registry.index","sha256":"` + hex.EncodeToString(digest[:]) + `"}`
	plain := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeTestResponse(t, writer, []byte("caller response"))
	}))
	t.Cleanup(plain.Close)
	secure := sameSchemeRegistryServer(t, sameSchemeRegistryServerOptions{
		metadata: metadata, indexBody: indexBody, downgradeURL: plain.URL,
	})
	caller := secure.Client()
	redirectCalls := 0
	caller.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		redirectCalls++
		return nil
	}
	client, err := New(Config{MetadataURL: secure.URL + "/registry.json", CacheDir: t.TempDir(), Client: caller})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// When
	index, err := client.FetchIndex(t.Context())
	if err != nil {
		t.Fatalf("FetchIndex() error = %v", err)
	}
	if _, statErr := os.Stat(index.Path); statErr != nil {
		t.Fatalf("cached index %q: %v", index.Path, statErr)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, secure.URL+"/downgrade", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext() error = %v", err)
	}
	response, err := caller.Do(request)
	if err != nil {
		t.Fatalf("caller client Do() error = %v", err)
	}
	if response == nil || response.Request == nil || response.Request.URL == nil || response.Body == nil {
		t.Fatal("caller client Do() returned an incomplete response")
	}
	t.Cleanup(func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("caller response Body.Close() error = %v", closeErr)
		}
	})

	// Then
	if redirectCalls != 2 {
		t.Fatalf("CheckRedirect calls = %d, want metadata and caller redirects", redirectCalls)
	}
	if response.StatusCode != http.StatusOK || response.Request.URL.Scheme != "http" {
		t.Fatalf("caller response = %d %s, want followed HTTP response", response.StatusCode, response.Request.URL)
	}
}

type sameSchemeRegistryServerOptions struct {
	metadata, downgradeURL string
	indexBody              []byte
}

func sameSchemeRegistryServer(t *testing.T, options sameSchemeRegistryServerOptions) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/registry.json":
			http.Redirect(writer, request, "/registry-final.json", http.StatusFound)
		case "/registry-final.json":
			writeTestResponse(t, writer, []byte(options.metadata))
		case "/registry.index":
			writeTestResponse(t, writer, options.indexBody)
		case "/downgrade":
			http.Redirect(writer, request, options.downgradeURL, http.StatusFound)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}
