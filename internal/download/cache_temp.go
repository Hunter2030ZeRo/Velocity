package download

import (
	"errors"
	"fmt"
	"os"
)

type cacheTemp struct {
	file    *os.File
	name    string
	cleanup bool
}

func (temp *cacheTemp) publish(path string) (bool, error) {
	if err := temp.file.Close(); err != nil {
		return false, fmt.Errorf("close cache temporary file: %w", err)
	}
	temp.file = nil
	if err := os.Link(temp.name, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("publish cache entry: %w", err)
	}
	if err := os.Remove(temp.name); err != nil {
		removePublishedErr := os.Remove(path)
		return false, errors.Join(
			fmt.Errorf("remove cache temporary link: %w", err),
			removePublishedErr,
		)
	}
	temp.cleanup = false
	return true, nil
}

func (temp *cacheTemp) finish(resultErr *error) {
	if temp.file != nil {
		if closeErr := temp.file.Close(); closeErr != nil && *resultErr == nil {
			*resultErr = fmt.Errorf("close cache temporary file: %w", closeErr)
		}
	}
	if !temp.cleanup {
		return
	}
	removeErr := os.Remove(temp.name)
	if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) && *resultErr == nil {
		*resultErr = fmt.Errorf("remove cache temporary file: %w", removeErr)
	}
}
