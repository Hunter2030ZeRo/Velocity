// Package registry fetches generated registry metadata and its verified index.
package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"github.com/Hunter2030ZeRo/velocity/internal/download"
)

const (
	defaultMaxMetadata = 64 << 10
	maxIndexBytes      = 16 << 20
)

// DefaultMetadataURL is the generated-branch registry metadata location.
const DefaultMetadataURL = "https://raw.githubusercontent.com/Hunter2030ZeRo/velocity-registry/registry/registry.json"

var (
	// ErrInvalidMetadataURL marks invalid or insecure registry metadata URLs.
	ErrInvalidMetadataURL = errors.New("registry: invalid metadata URL")
	// ErrInvalidMetadata marks malformed or unsupported registry metadata.
	ErrInvalidMetadata = errors.New("registry: invalid metadata")
	commitPattern      = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	digestPattern      = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)
	filenamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Config controls registry metadata and index retrieval.
type Config struct {
	Client           *http.Client
	MetadataURL      string
	CacheDir         string
	MaxMetadataBytes int64
	AllowHTTP        bool
}

// Index identifies a verified generated registry index.
type Index struct {
	Path   string
	Commit string
	SHA256 string
}

type fetcher interface {
	Fetch(context.Context, download.Artifact) (string, error)
}

// Client retrieves registry metadata and delegates index caching to download.
type Client struct {
	fetcher          fetcher
	metadataURL      *url.URL
	httpClient       *http.Client
	maxMetadataBytes int64
}

type metadata struct {
	Commit string `json:"commit"`
	Index  string `json:"index"`
	SHA256 string `json:"sha256"`
	Format int    `json:"format"`
}

// New constructs a registry client from validated configuration.
func New(config Config) (*Client, error) {
	if config.MaxMetadataBytes < 0 {
		return nil, fmt.Errorf("negative metadata size: %w", ErrInvalidMetadata)
	}
	metadataURL, err := parseMetadataURL(config.MetadataURL, config.AllowHTTP)
	if err != nil {
		return nil, err
	}
	httpClient := config.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	httpClient = configuredMetadataClient(httpClient, config.AllowHTTP)
	fetcher, err := download.New(download.Config{
		CacheDir:    config.CacheDir,
		Concurrency: 1,
		MaxBytes:    maxIndexBytes,
		Client:      httpClient,
		AllowHTTP:   config.AllowHTTP,
	})
	if err != nil {
		return nil, fmt.Errorf("create index downloader: %w", err)
	}
	maxMetadataBytes := config.MaxMetadataBytes
	if maxMetadataBytes == 0 {
		maxMetadataBytes = defaultMaxMetadata
	}
	return &Client{
		metadataURL:      metadataURL,
		httpClient:       httpClient,
		maxMetadataBytes: maxMetadataBytes,
		fetcher:          fetcher,
	}, nil
}

func configuredMetadataClient(source *http.Client, allowHTTP bool) *http.Client {
	client := *source
	redirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if (!allowHTTP && request.URL.Scheme != "https") ||
			(len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme == "http") {
			return fmt.Errorf("redirect to %s: %w", request.URL, ErrInvalidMetadataURL)
		}
		if redirect != nil {
			if err := redirect(request, via); err != nil {
				return err
			}
		}
		if len(via) >= 10 {
			return fmt.Errorf("redirect to %s: %w", request.URL, ErrInvalidMetadataURL)
		}
		return nil
	}
	return &client
}

// FetchIndex retrieves metadata, verifies the referenced index, and returns it.
func (c *Client) FetchIndex(ctx context.Context) (index Index, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.metadataURL.String(), nil)
	if err != nil {
		return Index{}, fmt.Errorf("create metadata request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Index{}, fmt.Errorf("fetch registry metadata: %w", err)
	}
	if response == nil {
		return Index{}, errors.New("fetch registry metadata: empty response")
	}
	if response.Request == nil || response.Request.URL == nil || response.Body == nil {
		return Index{}, errors.New("fetch registry metadata: incomplete response")
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			index = Index{}
			err = fmt.Errorf("close registry metadata response: %w", closeErr)
		}
	}()
	if c.metadataURL.Scheme == "https" && response.Request.URL.Scheme == "http" {
		return Index{}, fmt.Errorf(
			"fetch registry metadata: insecure final URL %q: %w", response.Request.URL, ErrInvalidMetadataURL,
		)
	}
	if response.StatusCode != http.StatusOK {
		return Index{}, fmt.Errorf("fetch registry metadata: unexpected HTTP status %d", response.StatusCode)
	}
	document, err := decodeMetadata(response.Body, c.maxMetadataBytes)
	if err != nil {
		return Index{}, err
	}
	indexURL := c.metadataURL.ResolveReference(&url.URL{Path: document.Index})
	path, err := c.fetcher.Fetch(ctx, download.Artifact{URL: indexURL.String(), SHA256: document.SHA256})
	if err != nil {
		return Index{}, fmt.Errorf("fetch registry index: %w", err)
	}
	return Index{Path: path, Commit: document.Commit, SHA256: document.SHA256}, nil
}

func parseMetadataURL(rawURL string, allowHTTP bool) (*url.URL, error) {
	if rawURL == "" {
		rawURL = DefaultMetadataURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse metadata URL: %w", ErrInvalidMetadataURL)
	}
	if parsed.Scheme != "https" && (!allowHTTP || parsed.Scheme != "http") {
		return nil, fmt.Errorf("metadata URL scheme: %w", ErrInvalidMetadataURL)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("metadata URL components: %w", ErrInvalidMetadataURL)
	}
	return parsed, nil
}

func decodeMetadata(body io.Reader, maxBytes int64) (metadata, error) {
	contents, readErr := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if readErr != nil {
		return metadata{}, fmt.Errorf("read registry metadata: %w", readErr)
	}
	if int64(len(contents)) > maxBytes {
		return metadata{}, fmt.Errorf("registry metadata exceeds %d bytes: %w", maxBytes, ErrInvalidMetadata)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var decoded metadata
	if decodeErr := decoder.Decode(&decoded); decodeErr != nil {
		return metadata{}, fmt.Errorf("decode registry metadata: %w", ErrInvalidMetadata)
	}
	var extra struct{}
	if extraErr := decoder.Decode(&extra); !errors.Is(extraErr, io.EOF) {
		return metadata{}, ErrInvalidMetadata
	}
	if validationErr := validateMetadata(decoded); validationErr != nil {
		return metadata{}, validationErr
	}
	return decoded, nil
}

func validateMetadata(value metadata) error {
	if value.Format != 1 || !commitPattern.MatchString(value.Commit) || !digestPattern.MatchString(value.SHA256) {
		return ErrInvalidMetadata
	}
	if !filenamePattern.MatchString(value.Index) {
		return ErrInvalidMetadata
	}
	return nil
}
