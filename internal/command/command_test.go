package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
	"github.com/Hunter2030ZeRo/velocity/internal/platform"
	"github.com/Hunter2030ZeRo/velocity/internal/registry"
)

func TestRoot_invokes_install_with_mapped_options_when_arguments_are_valid(t *testing.T) {
	// Given
	var got app.Options
	command := NewRoot(func(_ context.Context, options app.Options) (app.Result, error) {
		got = options
		return app.Result{}, nil
	})
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{
		"install", "ripgrep", "fd", "--root", "/install", "--cache", "/cache",
		"--resolver", "/resolver", "--registry", "https://registry.example/registry.json",
		"--jobs", "9", "--target", "x86_64-unknown-linux-musl",
	})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, []string{"ripgrep", "fd"}, got.Roots)
	require.Equal(t, platform.TargetX86_64LinuxMusl, got.Target)
	require.Equal(t, "/install", got.Root)
	require.Equal(t, "/cache", got.CacheDir)
	require.Equal(t, "/resolver", got.ResolverExecutable)
	require.Equal(t, "https://registry.example/registry.json", got.RegistryURL)
	require.Equal(t, 9, got.Jobs)
	require.Nil(t, got.HTTPClient)
	require.False(t, got.AllowHTTP)
}

func TestRoot_renders_deterministic_result_when_install_succeeds(t *testing.T) {
	// Given
	command := NewRoot(func(context.Context, app.Options) (app.Result, error) {
		return app.Result{
			RegistryCommit: "abc123",
			Packages: []app.InstalledPackage{
				{Name: "zeta", Version: "2.0.0"},
				{Name: "alpha", Version: "1.0.0"},
			},
			Installed: []string{"/install/bin/zeta", "/install/bin/alpha"},
		}, nil
	})
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"install", "alpha", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver"})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		"registry: abc123",
		"package: alpha@1.0.0",
		"package: zeta@2.0.0",
		"installed: /install/bin/alpha",
		"installed: /install/bin/zeta",
		"",
	}, "\n"), output.String())
}

func TestRoot_does_not_invoke_install_when_arguments_or_flags_are_invalid(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing packages", args: []string{"install"}},
		{name: "zero jobs", args: []string{"install", "fd", "--jobs", "0"}},
		{name: "negative jobs", args: []string{"install", "fd", "--jobs", "-1"}},
		{name: "invalid target", args: []string{"install", "fd", "--target", "darwin-arm64"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			called := false
			command := NewRoot(func(context.Context, app.Options) (app.Result, error) {
				called = true
				return app.Result{}, nil
			})
			command.SetArgs(test.args)

			// When
			err := command.ExecuteContext(context.Background())

			// Then
			require.Error(t, err)
			require.False(t, called)
		})
	}
}

func TestRoot_propagates_cancellation_to_install(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	command := NewRoot(func(ctx context.Context, _ app.Options) (app.Result, error) {
		called = true
		return app.Result{}, ctx.Err()
	})
	command.SetArgs([]string{"install", "fd", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver"})

	// When
	err := command.ExecuteContext(ctx)

	// Then
	require.True(t, called)
	require.ErrorIs(t, err, context.Canceled)
}

func TestRoot_supplies_nonempty_defaults_when_optional_flags_are_omitted(t *testing.T) {
	// Given
	var got app.Options
	command := NewRoot(func(_ context.Context, options app.Options) (app.Result, error) {
		got = options
		return app.Result{}, nil
	})
	command.SetArgs([]string{"install", "fd"})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.NoError(t, err)
	cacheBase, err := os.UserCacheDir()
	require.NoError(t, err)
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	wantRoot := filepath.Join(home, ".local")
	if runtime.GOOS == "windows" {
		wantRoot = filepath.Join(home, "AppData", "Local", "velocity")
	}
	wantTarget, err := platform.Detect(runtime.GOOS, runtime.GOARCH)
	require.NoError(t, err)
	require.Equal(t, wantRoot, got.Root)
	require.Equal(t, filepath.Join(cacheBase, "velocity"), got.CacheDir)
	require.Equal(t, filepath.Join(filepath.Dir(executable), resolverName()), got.ResolverExecutable)
	require.Equal(t, registry.DefaultMetadataURL, got.RegistryURL)
	require.Equal(t, wantTarget, got.Target)
	require.Equal(t, defaultJobs, got.Jobs)
}

func TestRoot_returns_install_error_when_application_fails(t *testing.T) {
	// Given
	want := errors.New("application failure")
	command := NewRoot(func(context.Context, app.Options) (app.Result, error) {
		return app.Result{}, want
	})
	command.SetArgs([]string{"install", "fd", "--root", "/install", "--cache", "/cache", "--resolver", "/resolver"})

	// When
	err := command.ExecuteContext(context.Background())

	// Then
	require.ErrorIs(t, err, want)
}
