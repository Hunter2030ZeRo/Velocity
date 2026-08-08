//go:build plan9 || js || wasip1

package download

import "os"

func validatePrivateOwner(_ string, _ os.FileInfo, _ bool) error {
	return nil
}
