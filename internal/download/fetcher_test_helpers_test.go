package download

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"testing"
)

func sha(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func mustNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustNew(t *testing.T, cfg Config) *Fetcher {
	t.Helper()
	fetcher, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher == nil {
		t.Fatal("New returned nil fetcher without an error")
	}
	return fetcher
}

func privateCacheDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "cache")
}

func writeResponse(t *testing.T, writer http.ResponseWriter, content string) {
	t.Helper()
	if _, err := io.WriteString(writer, content); err != nil {
		t.Errorf("write response: %v", err)
	}
}
