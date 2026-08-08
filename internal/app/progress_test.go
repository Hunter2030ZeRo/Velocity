package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
	"github.com/Hunter2030ZeRo/velocity/internal/download"
)

func Test_Install_reports_download_progress_until_all_artifacts_complete(t *testing.T) {
	// Given
	fixture := newRegistryFixture(t, fixtureOptions{})
	root := t.TempDir()
	t.Setenv(helperEnvironment, "1")
	t.Setenv(helperPlanEnvironment, fixture.plan)
	options := installOptions(root, fixture)
	var snapshots []download.Progress
	options.Progress = func(progress download.Progress) {
		snapshots = append(snapshots, progress)
	}

	// When
	_, err := app.Install(context.Background(), options)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, snapshots)
	last := snapshots[len(snapshots)-1]
	require.Positive(t, last.DownloadedBytes)
	require.Positive(t, last.TotalArtifacts)
	require.Equal(t, last.TotalArtifacts, last.CompletedArtifacts)
	require.Equal(t, 100, last.Percent)
}
