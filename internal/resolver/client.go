// Package resolver executes the versioned Rust dependency-resolution sidecar.
package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os/exec"
	"strings"
)

const protocolVersion uint32 = 1

// New validates configuration and creates a resolver sidecar client.
func New(config Config) (*Client, error) {
	if !isNonEmpty(config.Executable) {
		return nil, errors.New("resolver executable must not be empty")
	}
	if config.MaxOutputBytes <= 0 || config.MaxOutputBytes == math.MaxInt64 {
		return nil, errors.New("resolver max output bytes must be a positive bounded value")
	}
	return &Client{executable: config.Executable, maxOutputBytes: config.MaxOutputBytes}, nil
}

// Resolve validates request data, executes the sidecar, and validates its plan.
func (c *Client) Resolve(ctx context.Context, request Request) (Plan, error) {
	if ctx == nil {
		return Plan{}, errors.New("resolver context must not be nil")
	}
	if err := validateRequest(request); err != nil {
		return Plan{}, err
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	requestBytes, err := json.Marshal(sidecarRequest{
		Protocol: protocolVersion, IndexPath: request.IndexPath, Target: request.Target, Roots: request.Roots,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("encode request: %w", err)
	}

	// The executable is configured as a direct command path; no shell is involved.
	//nolint:gosec // command receives a configured executable path and no shell arguments are interpreted.
	command := exec.CommandContext(ctx, c.executable)
	command.Stdin = bytes.NewReader(requestBytes)
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return Plan{}, fmt.Errorf("open sidecar stdout: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return Plan{}, fmt.Errorf("open sidecar stderr: %w", err)
	}
	startErr := command.Start()
	if startErr != nil {
		return Plan{}, fmt.Errorf("start resolver sidecar: %w", startErr)
	}

	stdoutChannel := make(chan outputResult, 1)
	stderrChannel := make(chan outputResult, 1)
	go func() { stdoutChannel <- readOutput(stdoutPipe, c.maxOutputBytes) }()
	go func() { stderrChannel <- readOutput(stderrPipe, c.maxOutputBytes) }()
	stdout := <-stdoutChannel
	stderr := <-stderrChannel
	runErr := command.Wait()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Plan{}, ctxErr
	}
	if stdout.exceeded || stderr.exceeded {
		return Plan{}, outputLimitError(stdout.exceeded, stderr.exceeded)
	}
	if stdout.err != nil || stderr.err != nil {
		return Plan{}, fmt.Errorf("read sidecar output: %w", errors.Join(stdout.err, stderr.err))
	}
	if runErr != nil {
		return Plan{}, fmt.Errorf("resolver sidecar exited: %w: %s", runErr, strings.TrimSpace(string(stderr.data)))
	}
	return decodePlan(stdout.data, request.Target)
}

type sidecarRequest struct {
	IndexPath string   `json:"index_path"`
	Target    string   `json:"target"`
	Roots     []string `json:"roots"`
	Protocol  uint32   `json:"protocol"`
}

type outputResult struct {
	err      error
	data     []byte
	exceeded bool
}

func readOutput(reader io.ReadCloser, maxOutputBytes int64) (result outputResult) {
	defer func() {
		result.err = errors.Join(result.err, reader.Close())
	}()
	data, err := io.ReadAll(io.LimitReader(reader, maxOutputBytes+1))
	if err != nil {
		return outputResult{err: err}
	}
	if int64(len(data)) > maxOutputBytes {
		return outputResult{data: data[:int(maxOutputBytes)], exceeded: true}
	}
	return outputResult{data: data}
}

func outputLimitError(stdoutExceeded, stderrExceeded bool) error {
	switch {
	case stdoutExceeded && stderrExceeded:
		return errors.New("resolver stdout and stderr exceed configured output limit")
	case stdoutExceeded:
		return errors.New("resolver stdout exceeds configured output limit")
	default:
		return errors.New("resolver stderr exceeds configured output limit")
	}
}
