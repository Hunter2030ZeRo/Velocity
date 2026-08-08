// Package platform maps Go host identifiers to targets published by the
// Velocity registry.
package platform

import "fmt"

// Target is a target triple supported by the Velocity registry.
type Target string

// Target constants identify target triples supported by the Velocity registry.
const (
	TargetX86_64WindowsMSVC  Target = "x86_64-pc-windows-msvc"
	TargetAarch64WindowsMSVC Target = "aarch64-pc-windows-msvc"
	TargetX86_64LinuxGNU     Target = "x86_64-unknown-linux-gnu"
	TargetAarch64LinuxGNU    Target = "aarch64-unknown-linux-gnu"
	TargetX86_64LinuxMusl    Target = "x86_64-unknown-linux-musl"
	TargetAarch64LinuxMusl   Target = "aarch64-unknown-linux-musl"
)

// UnsupportedTargetError reports an OS, architecture, or target triple that
// is not part of the registry contract.
type UnsupportedTargetError struct {
	Input string
	OS    string
	Arch  string
}

func (e *UnsupportedTargetError) Error() string {
	if e.Input != "" {
		return fmt.Sprintf("unsupported target %q", e.Input)
	}
	return fmt.Sprintf("unsupported host %q/%q", e.OS, e.Arch)
}

// Parse converts a registry target triple into its typed representation.
func Parse(raw string) (Target, error) {
	switch Target(raw) {
	case TargetX86_64WindowsMSVC,
		TargetAarch64WindowsMSVC,
		TargetX86_64LinuxGNU,
		TargetAarch64LinuxGNU,
		TargetX86_64LinuxMusl,
		TargetAarch64LinuxMusl:
		return Target(raw), nil
	default:
		return "", &UnsupportedTargetError{Input: raw}
	}
}

// Detect maps Go's GOOS and GOARCH values to a registry target. Linux uses
// GNU by default; musl callers can select a musl target through Parse.
func Detect(goos, goarch string) (Target, error) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return TargetX86_64LinuxGNU, nil
		case "arm64":
			return TargetAarch64LinuxGNU, nil
		}
	case "windows":
		switch goarch {
		case "amd64":
			return TargetX86_64WindowsMSVC, nil
		case "arm64":
			return TargetAarch64WindowsMSVC, nil
		}
	}
	return "", &UnsupportedTargetError{OS: goos, Arch: goarch}
}

// String returns the target triple.
func (t Target) String() string { return string(t) }
