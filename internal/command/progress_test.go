package command

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
	"github.com/Hunter2030ZeRo/velocity/internal/download"
)

type descriptorWriter struct {
	io.Writer
	descriptor uintptr
}

func (writer descriptorWriter) Fd() uintptr {
	return writer.descriptor
}

type transientWriter struct {
	buffer bytes.Buffer
	writes int
}

func (writer *transientWriter) Write(buffer []byte) (int, error) {
	writer.writes++
	if writer.writes == 2 {
		return 0, io.ErrClosedPipe
	}
	return writer.buffer.Write(buffer)
}

func TestRoot_renders_download_progress_when_always_enabled(t *testing.T) {
	// Given
	command := NewRoot(func(_ context.Context, options app.Options) (app.Result, error) {
		require.NotNil(t, options.Progress)
		options.Progress(download.Progress{TotalArtifacts: 2})
		options.Progress(download.Progress{
			DownloadedBytes: 2 << 20, CompletedArtifacts: 1, TotalArtifacts: 2, Percent: 50,
		})
		options.Progress(download.Progress{
			DownloadedBytes: 4 << 20, CompletedArtifacts: 2, TotalArtifacts: 2, Percent: 100,
		})
		return app.Result{Packages: []app.InstalledPackage{{Name: "ripgrep", Version: "14.1.0"}}}, nil
	})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs([]string{
		"install", "ripgrep", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver",
		"--progress", "always",
	})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, "package: ripgrep@14.1.0\n", stdout.String())
	require.Contains(t, stderr.String(), "Downloading [")
	require.Contains(t, stderr.String(), "50%")
	require.Contains(t, stderr.String(), "1/2")
	require.Contains(t, stderr.String(), "100%")
	require.Contains(t, stderr.String(), "2/2")
	require.NotContains(t, stdout.String(), "Downloading")
}

func TestRoot_suppresses_automatic_progress_for_nonterminal_output(t *testing.T) {
	// Given
	command := NewRoot(func(_ context.Context, options app.Options) (app.Result, error) {
		require.Nil(t, options.Progress)
		return app.Result{}, nil
	})
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	command.SetOut(stdout)
	command.SetErr(stderr)
	command.SetArgs([]string{
		"install", "ripgrep", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver",
	})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())
}

func TestProgressEnabled_enables_auto_when_terminal_detector_accepts_descriptor(t *testing.T) {
	// Given
	const descriptor = uintptr(42)
	writer := descriptorWriter{Writer: &bytes.Buffer{}, descriptor: descriptor}

	// When
	enabled := progressEnabledWith(progressAuto, writer, func(value int) bool {
		return value == int(descriptor)
	})

	// Then
	require.True(t, enabled)
}

func TestRoot_suppresses_progress_when_never_enabled_for_terminal_output(t *testing.T) {
	// Given
	command := NewRoot(func(_ context.Context, options app.Options) (app.Result, error) {
		require.Nil(t, options.Progress)
		return app.Result{}, nil
	})
	stderr := &bytes.Buffer{}
	command.SetErr(descriptorWriter{Writer: stderr, descriptor: 42})
	command.SetArgs([]string{
		"install", "ripgrep", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver",
		"--progress", "never",
	})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	require.Empty(t, stderr.String())
}

func TestDownloadProgress_renders_indeterminate_state_without_percentage(t *testing.T) {
	// Given
	output := &bytes.Buffer{}
	renderer := newDownloadProgress(output)

	// When
	renderer.Report(download.Progress{
		DownloadedBytes: 2 << 20, TotalBytes: 2 << 20, TotalArtifacts: 1, Indeterminate: true,
	})
	err := renderer.Close()

	// Then
	require.NoError(t, err)
	require.Contains(t, output.String(), " ?%")
	require.NotContains(t, output.String(), "  0%")
}

func TestDownloadProgress_suppresses_empty_batch(t *testing.T) {
	// Given
	output := &bytes.Buffer{}
	renderer := newDownloadProgress(output)

	// When
	renderer.Report(download.Progress{TotalArtifacts: 0, Percent: 100})
	err := renderer.Close()

	// Then
	require.NoError(t, err)
	require.Empty(t, output.String())
}

func TestDownloadProgress_terminates_open_line_after_transient_write_failure(t *testing.T) {
	// Given
	output := &transientWriter{}
	renderer := newDownloadProgress(output)
	renderer.Report(download.Progress{TotalArtifacts: 1})

	// When
	renderer.Report(download.Progress{DownloadedBytes: 1 << 20, TotalArtifacts: 1, Percent: 50})
	err := renderer.Close()

	// Then
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Equal(t, 3, output.writes)
	require.True(t, strings.HasSuffix(output.buffer.String(), "\n"))
}

func TestRoot_rejects_unknown_progress_mode_before_install(t *testing.T) {
	// Given
	called := false
	command := NewRoot(func(context.Context, app.Options) (app.Result, error) {
		called = true
		return app.Result{}, nil
	})
	command.SetArgs([]string{"install", "ripgrep", "--progress", "sometimes"})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.ErrorContains(t, err, "progress mode")
	require.False(t, called)
}
