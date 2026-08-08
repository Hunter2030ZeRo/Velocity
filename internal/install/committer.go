// Package install commits validated staged binaries into a Velocity installation.
package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrInvalidConfig reports an unusable committer configuration.
	ErrInvalidConfig = errors.New("install: invalid config")
	// ErrInvalidBinary reports an invalid staged binary.
	ErrInvalidBinary = errors.New("install: invalid staged binary")
	// ErrDestinationExists reports an install destination that already exists.
	ErrDestinationExists = errors.New("install: destination exists")
)

// StagedBinary identifies a binary extracted into a staging directory.
type StagedBinary struct {
	Name string
	Path string
}

// Config configures a Committer.
type Config struct {
	Root string
}

// Committer moves staged binaries into its configured bin directory.
type Committer struct {
	root string
}

// New constructs a Committer that installs binaries beneath config.Root.
func New(config Config) (*Committer, error) {
	if config.Root == "" {
		return nil, fmt.Errorf("root is empty: %w", ErrInvalidConfig)
	}
	return &Committer{root: filepath.Clean(config.Root)}, nil
}

// Commit installs binaries into <Root>/bin and returns their paths in name order.
func (c *Committer) Commit(ctx context.Context, binaries []StagedBinary) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("commit cancelled before preflight: %w", err)
	}

	ordered, err := validateStagedBinaries(ctx, binaries)
	if err != nil {
		return nil, err
	}
	if len(ordered) == 0 {
		return []string{}, nil
	}

	binDir := filepath.Join(c.root, "bin")
	if err = validateBinDirectory(binDir); err != nil {
		return nil, err
	}
	if err = validateDestinations(ctx, binDir, ordered); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, fmt.Errorf("commit cancelled before mutation: %w", err)
	}
	if err = createBinDirectory(binDir); err != nil {
		return nil, err
	}

	moved := make([]StagedBinary, 0, len(ordered))
	for _, binary := range ordered {
		if err = moveNoReplace(ctx, binary.Path, filepath.Join(binDir, binary.Name)); err != nil {
			return nil, errors.Join(
				fmt.Errorf("install binary %q: %w", binary.Name, err),
				rollback(binDir, moved),
			)
		}
		moved = append(moved, binary)
	}

	installed := make([]string, len(ordered))
	for index, binary := range ordered {
		installed[index] = filepath.Join(binDir, binary.Name)
	}
	return installed, nil
}

func validateStagedBinaries(ctx context.Context, binaries []StagedBinary) ([]StagedBinary, error) {
	ordered := append([]StagedBinary(nil), binaries...)
	sort.Slice(ordered, func(left int, right int) bool {
		return ordered[left].Name < ordered[right].Name
	})

	names := make(map[string]struct{}, len(ordered))
	for _, binary := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("commit cancelled during preflight: %w", err)
		}
		if !isBasename(binary.Name) {
			return nil, fmt.Errorf("binary name %q: %w", binary.Name, ErrInvalidBinary)
		}
		if _, exists := names[binary.Name]; exists {
			return nil, fmt.Errorf("duplicate binary name %q: %w", binary.Name, ErrInvalidBinary)
		}
		names[binary.Name] = struct{}{}

		info, err := os.Lstat(binary.Path)
		if err != nil {
			return nil, fmt.Errorf("inspect staged binary %q: %w", binary.Name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("staged binary %q is not a regular file: %w", binary.Name, ErrInvalidBinary)
		}
	}
	return ordered, nil
}

func isBasename(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\\`)
}

func validateBinDirectory(binDir string) error {
	info, err := os.Lstat(binDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect bin directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bin directory is not a directory: %w", ErrInvalidConfig)
	}
	return nil
}

func validateDestinations(ctx context.Context, binDir string, binaries []StagedBinary) error {
	for _, binary := range binaries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("commit cancelled during destination preflight: %w", err)
		}
		_, err := os.Lstat(filepath.Join(binDir, binary.Name))
		if err == nil {
			return fmt.Errorf("destination for %q: %w", binary.Name, ErrDestinationExists)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect destination for %q: %w", binary.Name, err)
		}
	}
	return nil
}

func createBinDirectory(binDir string) error {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return fmt.Errorf("create bin directory: %w", err)
	}
	return validateBinDirectory(binDir)
}

func moveNoReplace(ctx context.Context, source string, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("link staged binary: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, removeNewDestination(destination))
	}
	if err := os.Remove(source); err != nil {
		return errors.Join(fmt.Errorf("remove staged binary: %w", err), removeNewDestination(destination))
	}
	return nil
}

func removeNewDestination(destination string) error {
	if err := os.Remove(destination); err != nil {
		return fmt.Errorf("remove destination during cleanup: %w", err)
	}
	return nil
}

func rollback(binDir string, binaries []StagedBinary) error {
	var rollbackErr error
	for index := len(binaries) - 1; index >= 0; index-- {
		binary := binaries[index]
		if err := restoreNoReplace(filepath.Join(binDir, binary.Name), binary.Path); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore binary %q: %w", binary.Name, err))
		}
	}
	return rollbackErr
}

func restoreNoReplace(source string, destination string) error {
	if err := os.Link(source, destination); err != nil {
		return fmt.Errorf("link installed binary: %w", err)
	}
	if err := os.Remove(source); err != nil {
		return errors.Join(fmt.Errorf("remove installed binary: %w", err), removeNewDestination(destination))
	}
	return nil
}
