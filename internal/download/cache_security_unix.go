//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package download

import (
	"fmt"
	"os"
	"syscall"
)

func validatePrivateOwner(_ string, info os.FileInfo, _ bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) {
		return fmt.Errorf("cache path must be owned by the current user: %w", ErrUnsafeCache)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf(
			"cache path permissions %04o allow modification by another user: %w",
			info.Mode().Perm(),
			ErrUnsafeCache,
		)
	}
	return nil
}
