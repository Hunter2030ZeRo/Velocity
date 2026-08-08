package download

import (
	"fmt"
	"os"
)

func validateCacheDirectory(path string) (err error) {
	entry, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect cache directory: %w", err)
	}
	if entry.Mode()&os.ModeSymlink != 0 || !entry.IsDir() {
		return fmt.Errorf("cache directory must be a real directory: %w", ErrUnsafeCache)
	}
	if validationErr := validatePrivateOwner(path, entry, true); validationErr != nil {
		return validationErr
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open cache directory: %w", err)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close cache directory: %w", closeErr)
		}
	}()
	opened, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened cache directory: %w", err)
	}
	if !os.SameFile(entry, opened) {
		return fmt.Errorf("cache directory changed while opening: %w", ErrUnsafeCache)
	}
	return nil
}

func validateCacheEntry(path string, entry os.FileInfo) error {
	if entry.Mode()&os.ModeSymlink != 0 || !entry.Mode().IsRegular() {
		return fmt.Errorf("cache entry must be a regular file: %w", ErrUnsafeCache)
	}
	return validatePrivateOwner(path, entry, false)
}
