package resolver

import "fmt"

// Request is the input sent to the Rust resolver sidecar.
type Request struct {
	IndexPath string
	Target    string
	Roots     []string
}

// Binary describes one executable contained in a resolved artifact.
type Binary struct {
	Source string
	Name   string
}

// Artifact describes how to obtain and unpack a resolved package.
type Artifact struct {
	Target          string
	URL             string
	SHA256          string
	Archive         string
	Binaries        []Binary
	StripComponents uint32
}

// Package is one package selected by the resolver.
type Package struct {
	Name     string
	Version  string
	Artifact Artifact
	ID       uint64
}

// Plan contains the selected packages in installation order.
type Plan struct {
	Packages []Package
}

// Config configures a Client.
type Config struct {
	Executable     string
	MaxOutputBytes int64
}

// Client resolves packages through a single sidecar invocation per request.
type Client struct {
	executable     string
	maxOutputBytes int64
}

// ResolutionError is an error returned by the resolver sidecar.
type ResolutionError struct {
	Code    string
	Message string
}

func (e *ResolutionError) Error() string {
	return fmt.Sprintf("resolver: %s: %s", e.Code, e.Message)
}
