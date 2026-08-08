package platform

import (
	"errors"
	"testing"
)

func TestParse_returns_each_registry_supported_target(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Target
	}{
		{name: "x86_64 windows msvc", input: "x86_64-pc-windows-msvc", want: TargetX86_64WindowsMSVC},
		{name: "aarch64 windows msvc", input: "aarch64-pc-windows-msvc", want: TargetAarch64WindowsMSVC},
		{name: "x86_64 linux gnu", input: "x86_64-unknown-linux-gnu", want: TargetX86_64LinuxGNU},
		{name: "aarch64 linux gnu", input: "aarch64-unknown-linux-gnu", want: TargetAarch64LinuxGNU},
		{name: "x86_64 linux musl", input: "x86_64-unknown-linux-musl", want: TargetX86_64LinuxMusl},
		{name: "aarch64 linux musl", input: "aarch64-unknown-linux-musl", want: TargetAarch64LinuxMusl},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			// When
			got, err := Parse(tt.input)
			// Then
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Parse() = %q, want %q", got, tt.want)
			}
			if got.String() != tt.input {
				t.Fatalf("Target.String() = %q, want %q", got.String(), tt.input)
			}
		})
	}
}

func TestParse_returns_typed_error_for_unsupported_target(t *testing.T) {
	// Given
	input := "x86_64-apple-darwin"

	// When
	_, err := Parse(input)

	// Then
	var unsupported *UnsupportedTargetError
	if !errors.As(err, &unsupported) {
		t.Fatalf("Parse() error = %T %v, want *UnsupportedTargetError", err, err)
	}
	if unsupported.Input != input {
		t.Fatalf("UnsupportedTargetError.Input = %q, want %q", unsupported.Input, input)
	}
}

func TestDetect_selects_registry_target_for_supported_host(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
		want   Target
	}{
		{name: "x86_64 linux defaults to gnu", goos: "linux", goarch: "amd64", want: TargetX86_64LinuxGNU},
		{name: "aarch64 linux defaults to gnu", goos: "linux", goarch: "arm64", want: TargetAarch64LinuxGNU},
		{name: "x86_64 windows defaults to msvc", goos: "windows", goarch: "amd64", want: TargetX86_64WindowsMSVC},
		{name: "aarch64 windows defaults to msvc", goos: "windows", goarch: "arm64", want: TargetAarch64WindowsMSVC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			// When
			got, err := Detect(tt.goos, tt.goarch)
			// Then
			if err != nil {
				t.Fatalf("Detect() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetect_returns_typed_error_for_unsupported_host(t *testing.T) {
	tests := []struct {
		name   string
		goos   string
		goarch string
	}{
		{name: "unsupported operating system", goos: "darwin", goarch: "amd64"},
		{name: "unsupported architecture", goos: "linux", goarch: "386"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			// When
			_, err := Detect(tt.goos, tt.goarch)

			// Then
			var unsupported *UnsupportedTargetError
			if !errors.As(err, &unsupported) {
				t.Fatalf("Detect() error = %T %v, want *UnsupportedTargetError", err, err)
			}
			if unsupported.OS != tt.goos || unsupported.Arch != tt.goarch {
				t.Fatalf("UnsupportedTargetError = {%q, %q}, want {%q, %q}", unsupported.OS, unsupported.Arch, tt.goos, tt.goarch)
			}
		})
	}
}
