// Package download fetches verified artifacts into a local content-addressed cache.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"

	"golang.org/x/sync/singleflight"
)

const defaultMaxArtifacts = 256

var (
	// ErrInvalidConfig reports an invalid fetcher configuration.
	ErrInvalidConfig = errors.New("download: invalid config")
	// ErrInvalidArtifact reports an invalid artifact URL or digest.
	ErrInvalidArtifact = errors.New("download: invalid artifact")
	// ErrInsecureURL reports a URL or redirect that violates the transport policy.
	ErrInsecureURL = errors.New("download: insecure URL")
	// ErrTooLarge reports an artifact that exceeds the configured maximum size.
	ErrTooLarge = errors.New("download: artifact exceeds maximum size")
	// ErrChecksum reports a downloaded artifact whose digest does not match its expected digest.
	ErrChecksum = errors.New("download: checksum mismatch")
	// ErrFetchLimit reports a FetchAll transaction that exceeds its byte budget.
	ErrFetchLimit = errors.New("download: fetch transaction exceeds byte budget")
	// ErrArtifactLimit reports a FetchAll transaction with too many artifacts.
	ErrArtifactLimit = errors.New("download: fetch transaction exceeds artifact limit")
	// ErrUnsafeCache reports a cache directory or entry that is unsafe to consume.
	ErrUnsafeCache = errors.New("download: unsafe cache path")
)

// Config controls how a Fetcher stores and downloads artifacts.
type Config struct {
	Client        *http.Client
	Progress      ProgressFunc
	CacheDir      string
	MaxBytes      int64
	MaxTotalBytes int64
	MaxArtifacts  int
	Concurrency   int
	AllowHTTP     bool
}

// Artifact identifies a download and its expected SHA-256 digest.
type Artifact struct {
	URL    string
	SHA256 string
}

// Fetcher downloads verified artifacts into a content-addressed cache.
type Fetcher struct {
	flights       singleflight.Group
	client        *http.Client
	progress      *progressDispatcher
	cacheDir      string
	maxBytes      int64
	maxTotalBytes int64
	maxArtifacts  int
	concurrency   int
	allowHTTP     bool
	batchID       atomic.Uint64
}

type parsedArtifact struct {
	url      *url.URL
	digest   string
	expected [sha256.Size]byte
}

// New constructs a Fetcher with a bounded, redirect-safe HTTP client.
func New(cfg Config) (*Fetcher, error) {
	if cfg.CacheDir == "" || cfg.Concurrency < 1 || cfg.MaxBytes < 1 || cfg.MaxBytes == math.MaxInt64 {
		return nil, fmt.Errorf("cache directory, positive concurrency, and bounded maximum bytes: %w", ErrInvalidConfig)
	}
	client, err := configuredClient(cfg.Client, cfg.AllowHTTP)
	if err != nil {
		return nil, fmt.Errorf("configure HTTP client: %w", err)
	}
	maxTotalBytes := cfg.MaxTotalBytes
	if maxTotalBytes == 0 {
		maxTotalBytes = cfg.MaxBytes
	}
	maxArtifacts := cfg.MaxArtifacts
	if maxArtifacts == 0 {
		maxArtifacts = defaultMaxArtifacts
	}
	if maxTotalBytes < 1 || maxTotalBytes == math.MaxInt64 || maxArtifacts < 1 {
		return nil, fmt.Errorf("positive bounded FetchAll limits: %w", ErrInvalidConfig)
	}
	return &Fetcher{
		cacheDir:      cfg.CacheDir,
		concurrency:   cfg.Concurrency,
		maxBytes:      cfg.MaxBytes,
		maxTotalBytes: maxTotalBytes,
		maxArtifacts:  maxArtifacts,
		client:        client,
		progress:      newProgressDispatcher(cfg.Progress),
		allowHTTP:     cfg.AllowHTTP,
	}, nil
}

// Fetch downloads artifact unless a verified cached copy already exists.
func (f *Fetcher) Fetch(ctx context.Context, artifact Artifact) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parsed, err := f.parse(artifact)
	if err != nil {
		return "", err
	}
	result, err := f.fetchShared(ctx, sharedFetch{key: parsed.digest, artifact: parsed})
	if err != nil {
		return "", err
	}
	return result.path, nil
}

func (f *Fetcher) parse(artifact Artifact) (parsedArtifact, error) {
	digest, err := hex.DecodeString(artifact.SHA256)
	if err != nil || len(artifact.SHA256) != sha256.Size*2 || len(digest) != sha256.Size {
		return parsedArtifact{}, fmt.Errorf("SHA-256 %q: %w", artifact.SHA256, ErrInvalidArtifact)
	}
	u, err := url.ParseRequestURI(artifact.URL)
	if err != nil {
		return parsedArtifact{}, fmt.Errorf("URL %q: %w", artifact.URL, ErrInsecureURL)
	}
	isSecure := u.Scheme == "https"
	isAllowedHTTP := f.allowHTTP && u.Scheme == "http"
	if u.Host == "" || (!isSecure && !isAllowedHTTP) {
		return parsedArtifact{}, fmt.Errorf("URL %q: %w", artifact.URL, ErrInsecureURL)
	}
	var expected [sha256.Size]byte
	copy(expected[:], digest)
	return parsedArtifact{url: u, digest: strings.ToLower(artifact.SHA256), expected: expected}, nil
}
