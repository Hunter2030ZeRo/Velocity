package install

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestCommit_installs_executable_binaries_in_name_order(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	beta := writeStageFile(t, stageDir, stageFileFixture{name: "beta", content: "beta"})
	alpha := writeStageFile(t, stageDir, stageFileFixture{name: "alpha", content: "alpha"})
	committer := newCommitter(t, root)

	// When
	installed, err := committer.Commit(t.Context(), []StagedBinary{
		{Name: "beta", Path: beta},
		{Name: "alpha", Path: alpha},
	})

	// Then
	require.NoError(t, err)
	assertStringsEqual(t, []string{
		filepath.Join(root, "bin", "alpha"),
		filepath.Join(root, "bin", "beta"),
	}, installed)
	assertFile(t, filepath.Join(root, "bin", "alpha"), "alpha")
	assertFile(t, filepath.Join(root, "bin", "beta"), "beta")
	binInfo, err := os.Stat(filepath.Join(root, "bin"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o750), binInfo.Mode().Perm())
	_, err = os.Lstat(alpha)
	assertTrue(t, errors.Is(err, os.ErrNotExist))
	_, err = os.Lstat(beta)
	assertTrue(t, errors.Is(err, os.ErrNotExist))
}

func TestCommit_leaves_every_staged_binary_untouched_when_destination_collides(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	alpha := writeStageFile(t, stageDir, stageFileFixture{name: "alpha", content: "alpha"})
	beta := writeStageFile(t, stageDir, stageFileFixture{name: "beta", content: "beta"})
	binDir := filepath.Join(root, "bin")
	require.NoError(t, os.Mkdir(binDir, 0o755))
	writeStageFile(t, binDir, stageFileFixture{name: "beta", content: "existing"})
	committer := newCommitter(t, root)
	var err error

	// When
	_, err = committer.Commit(t.Context(), []StagedBinary{
		{Name: "alpha", Path: alpha},
		{Name: "beta", Path: beta},
	})

	// Then
	assertTrue(t, errors.Is(err, ErrDestinationExists))
	assertFile(t, alpha, "alpha")
	assertFile(t, beta, "beta")
	assertFile(t, filepath.Join(binDir, "beta"), "existing")
	_, err = os.Lstat(filepath.Join(binDir, "alpha"))
	assertTrue(t, errors.Is(err, os.ErrNotExist))
}

func TestCommit_rejects_invalid_staged_binary_when_source_is_symlink(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	target := writeStageFile(t, stageDir, stageFileFixture{name: "target", content: "binary"})
	link := filepath.Join(stageDir, "binary")
	require.NoError(t, os.Symlink(target, link))
	committer := newCommitter(t, root)
	var err error

	// When
	_, err = committer.Commit(t.Context(), []StagedBinary{{Name: "binary", Path: link}})

	// Then
	assertTrue(t, errors.Is(err, ErrInvalidBinary))
	info, statErr := os.Lstat(link)
	require.NoError(t, statErr)
	assertTrue(t, info.Mode()&os.ModeSymlink != 0)
	_, statErr = os.Lstat(filepath.Join(root, "bin", "binary"))
	assertTrue(t, errors.Is(statErr, os.ErrNotExist))
}

func TestCommit_rejects_staged_binary_when_name_is_not_a_basename(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	binary := writeStageFile(t, stageDir, stageFileFixture{name: "binary", content: "binary"})
	committer := newCommitter(t, root)
	var err error

	// When
	_, err = committer.Commit(t.Context(), []StagedBinary{{Name: "nested/binary", Path: binary}})

	// Then
	assertTrue(t, errors.Is(err, ErrInvalidBinary))
	assertFile(t, binary, "binary")
	_, statErr := os.Lstat(filepath.Join(root, "bin", "binary"))
	assertTrue(t, errors.Is(statErr, os.ErrNotExist))
}

func TestCommit_rejects_staged_binaries_when_names_are_not_unique(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	first := writeStageFile(t, stageDir, stageFileFixture{name: "first", content: "first"})
	second := writeStageFile(t, stageDir, stageFileFixture{name: "second", content: "second"})
	committer := newCommitter(t, root)
	var err error

	// When
	_, err = committer.Commit(t.Context(), []StagedBinary{
		{Name: "binary", Path: first},
		{Name: "binary", Path: second},
	})

	// Then
	assertTrue(t, errors.Is(err, ErrInvalidBinary))
	assertFile(t, first, "first")
	assertFile(t, second, "second")
}

func TestCommit_leaves_staged_binary_untouched_when_context_is_cancelled(t *testing.T) {
	// Given
	root := t.TempDir()
	stageDir := t.TempDir()
	binary := writeStageFile(t, stageDir, stageFileFixture{name: "binary", content: "binary"})
	committer := newCommitter(t, root)
	var err error
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// When
	_, err = committer.Commit(ctx, []StagedBinary{{Name: "binary", Path: binary}})

	// Then
	assertTrue(t, errors.Is(err, context.Canceled))
	assertFile(t, binary, "binary")
	_, statErr := os.Lstat(filepath.Join(root, "bin"))
	assertTrue(t, errors.Is(statErr, os.ErrNotExist))
}

type stageFileFixture struct {
	name, content string
}

func writeStageFile(t *testing.T, dir string, fixture stageFileFixture) string {
	t.Helper()
	path := filepath.Join(dir, fixture.name)
	require.NoError(t, os.WriteFile(path, []byte(fixture.content), 0o755))
	return path
}

func assertFile(t *testing.T, path string, wantContent string) {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, wantContent, string(content))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func newCommitter(t *testing.T, root string) *Committer {
	t.Helper()
	committer, err := New(Config{Root: root})
	require.NoError(t, err)
	return committer
}

func assertTrue(t *testing.T, value bool) {
	t.Helper()
	require.True(t, value)
}

func assertStringsEqual(t *testing.T, want []string, got []string) {
	t.Helper()
	require.True(t, slices.Equal(want, got), "got %v, want %v", got, want)
}
