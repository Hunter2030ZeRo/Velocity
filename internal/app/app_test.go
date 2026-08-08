package app_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
	"github.com/Hunter2030ZeRo/velocity/internal/download"
	"github.com/Hunter2030ZeRo/velocity/internal/platform"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvironment) == "1" {
		runResolverHelper()
		return
	}
	goleak.VerifyTestMain(m)
}

func Test_Install_commits_no_binaries_when_artifact_is_corrupt(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{corruptArtifact: true})
	root := t.TempDir()
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)

	// When
	_, err := app.Install(context.Background(), installOptions(root, fixture))

	// Then
	require.ErrorIs(t, err, download.ErrChecksum)
	require.NoDirExists(t, filepath.Join(root, "bin"))
}

func Test_Install_preserves_existing_destination_when_commit_collides(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{})
	root := t.TempDir()
	existing := filepath.Join(root, "bin", "root")
	require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o755))
	require.NoError(t, os.WriteFile(existing, []byte("user file"), 0o755))
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)

	// When
	_, err := app.Install(context.Background(), installOptions(root, fixture))

	// Then
	require.Error(t, err)
	contents, readErr := os.ReadFile(existing)
	require.NoError(t, readErr)
	require.Equal(t, []byte("user file"), contents)
}

func Test_Install_removes_staged_data_when_commit_collides(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{})
	root := t.TempDir()
	existing := filepath.Join(root, "bin", "root")
	require.NoError(t, os.MkdirAll(filepath.Dir(existing), 0o755))
	require.NoError(t, os.WriteFile(existing, []byte("user file"), 0o755))
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)

	// When
	_, err := app.Install(context.Background(), installOptions(root, fixture))

	// Then
	require.Error(t, err)
	entries, readErr := os.ReadDir(filepath.Join(root, ".velocity", "staging"))
	require.NoError(t, readErr)
	require.Empty(t, entries)
}

func Test_Install_returns_context_cancellation_when_pre_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	_, err := app.Install(ctx, app.Options{})

	// Then
	require.ErrorIs(t, err, context.Canceled)
}

func Test_Install_rejects_empty_root_before_external_work_or_cwd_side_effects(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{})
	workingDir := t.TempDir()
	resolverMarker := filepath.Join(t.TempDir(), "resolver-invoked")
	t.Chdir(workingDir)
	transport := &countingRoundTripper{base: fixture.client.Transport}
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)
	t.Setenv(helperInvocationEnvironment, resolverMarker)
	options := installOptions("", fixture)
	options.HTTPClient = &http.Client{Transport: transport}

	// When
	_, err := app.Install(context.Background(), options)

	// Then
	assert.ErrorContains(t, err, "configure committer")
	assert.Zero(t, transport.requests.Load())
	assert.NoFileExists(t, resolverMarker)
	assert.NoDirExists(t, filepath.Join(workingDir, ".velocity"))
}

type countingRoundTripper struct {
	base     http.RoundTripper
	requests atomic.Int64
}

func (r *countingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	r.requests.Add(1)
	return r.base.RoundTrip(request)
}

func installOptions(root string, fixture registryFixture) app.Options {
	return app.Options{
		Roots: []string{"root"}, Target: platform.TargetX86_64LinuxGNU,
		Root: root, CacheDir: filepath.Join(root, "cache"), ResolverExecutable: os.Args[0],
		RegistryURL: fixture.metadataURL, Jobs: 2, HTTPClient: fixture.client, AllowHTTP: true,
	}
}

func Test_Install_installs_raw_and_archive_binaries_in_dependency_order(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{})
	root := t.TempDir()
	fixture.wantResult.Installed = []string{filepath.Join(root, "bin", "dep"), filepath.Join(root, "bin", "root")}
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)

	// When
	result, err := app.Install(context.Background(), app.Options{
		Roots: []string{"root"}, Target: platform.TargetX86_64LinuxGNU,
		Root: root, CacheDir: filepath.Join(root, "cache"), ResolverExecutable: os.Args[0],
		RegistryURL: fixture.metadataURL, Jobs: 2, HTTPClient: fixture.client, AllowHTTP: true,
	})

	// Then
	require.NoError(t, err)
	require.Equal(t, fixture.wantResult, result)
	for _, name := range []string{"dep", "root"} {
		path := filepath.Join(root, "bin", name)
		require.FileExists(t, path)
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.NotZero(t, info.Mode().Perm()&0o111)
	}
}
