package resolver

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

const helperEnvironment = "VELOCITY_RESOLVER_HELPER"

func TestMain(m *testing.M) {
	if os.Getenv(helperEnvironment) == "1" {
		runHelperProcess()
		return
	}
	goleak.VerifyTestMain(m)
}

func TestClient_Resolve_returns_plan_when_sidecar_returns_valid_response(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "ok")
	client := newHelperClient(t, 1024)

	// When
	plan, err := client.Resolve(context.Background(), validRequest())
	// Then
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(plan.Packages) != 1 {
		t.Fatalf("Resolve() package count = %d, want 1", len(plan.Packages))
	}
	if got := plan.Packages[0].Artifact.Binaries[0].Name; got != "velocity" {
		t.Fatalf("Resolve() binary name = %q, want velocity", got)
	}
}

func TestClient_Resolve_rejects_artifact_for_different_target(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "mismatched-target")
	client := newHelperClient(t, 1024)

	// When
	_, err := client.Resolve(context.Background(), validRequest())

	// Then
	if err == nil || !strings.Contains(err.Error(), "artifact target") {
		t.Fatalf("Resolve() error = %v, want artifact target mismatch error", err)
	}
}

func TestClient_Resolve_rejects_unsafe_registry_metadata(t *testing.T) {
	// Given
	unsafeModes := []string{
		"package-name-c0", "package-name-c1", "package-name-escape", "package-name-del", "package-name-invalid-utf8",
		"package-version-c0", "package-version-c1", "package-version-escape", "package-version-del", "package-version-invalid-utf8",
		"binary-source-c0", "binary-source-c1", "binary-source-escape", "binary-source-del", "binary-source-invalid-utf8",
		"binary-name-c0", "binary-name-c1", "binary-name-escape", "binary-name-del", "binary-name-invalid-utf8",
	}
	for _, mode := range unsafeModes {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(helperEnvironment, "1")
			t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", mode)
			client := newHelperClient(t, 1024)

			// When
			_, err := client.Resolve(context.Background(), validRequest())

			// Then
			if err == nil {
				t.Fatal("Resolve() error = nil, want unsafe metadata rejection")
			}
		})
	}
}

func TestClient_Resolve_preserves_printable_registry_metadata(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "printable")
	client := newHelperClient(t, 1024)

	// When
	plan, err := client.Resolve(context.Background(), validRequest())
	// Then
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	packageValue := plan.Packages[0]
	if packageValue.Name != "velocity-tools" || packageValue.Version != "1.2.3-alpha.1+build.7" {
		t.Fatalf("package metadata = %q@%q, want printable registry metadata", packageValue.Name, packageValue.Version)
	}
	binary := packageValue.Artifact.Binaries[0]
	if binary.Source != "bin/tools/velocity" || binary.Name != "velocity" {
		t.Fatalf("binary metadata = %#v, want portable path and name", binary)
	}
}

func TestClient_Resolve_rejects_unsafe_binary_paths(t *testing.T) {
	// Given
	unsafeModes := []string{"binary-source-parent", "binary-source-backslash", "binary-name-parent", "binary-name-backslash"}
	for _, mode := range unsafeModes {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(helperEnvironment, "1")
			t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", mode)
			client := newHelperClient(t, 1024)

			// When
			_, err := client.Resolve(context.Background(), validRequest())

			// Then
			if err == nil {
				t.Fatal("Resolve() error = nil, want unsafe binary path rejection")
			}
		})
	}
}

func TestClient_Resolve_returns_resolution_error_when_sidecar_returns_structured_error(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "resolution-error")
	client := newHelperClient(t, 1024)

	// When
	_, err := client.Resolve(context.Background(), validRequest())

	// Then
	var resolutionError *ResolutionError
	if !errors.As(err, &resolutionError) {
		t.Fatalf("Resolve() error = %T %v, want *ResolutionError", err, err)
	}
	if resolutionError.Code != "package_not_found" {
		t.Fatalf("ResolutionError.Code = %q, want package_not_found", resolutionError.Code)
	}
}

func TestClient_Resolve_rejects_malformed_response(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "malformed")
	client := newHelperClient(t, 1024)

	// When
	_, err := client.Resolve(context.Background(), validRequest())

	// Then
	if err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("Resolve() error = %v, want decode response error", err)
	}
}

func TestClient_Resolve_rejects_oversized_response(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "oversized")
	client := newHelperClient(t, 32)

	// When
	_, err := client.Resolve(context.Background(), validRequest())

	// Then
	if err == nil || !strings.Contains(err.Error(), "stdout exceeds") {
		t.Fatalf("Resolve() error = %v, want bounded stdout error", err)
	}
}

func TestClient_Resolve_reports_nonzero_exit_stderr(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "nonzero")
	client := newHelperClient(t, 1024)

	// When
	_, err := client.Resolve(context.Background(), validRequest())

	// Then
	if err == nil || !strings.Contains(err.Error(), "sidecar exited") || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("Resolve() error = %v, want exit status and stderr", err)
	}
}

func TestClient_Resolve_returns_context_error_when_context_is_cancelled(t *testing.T) {
	// Given
	t.Setenv(helperEnvironment, "1")
	t.Setenv("VELOCITY_RESOLVER_HELPER_MODE", "block")
	client := newHelperClient(t, 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// When
	_, err := client.Resolve(ctx, validRequest())

	// Then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve() error = %v, want context.DeadlineExceeded", err)
	}
}

func TestClient_New_rejects_invalid_config(t *testing.T) {
	// Given
	config := Config{MaxOutputBytes: 1}

	// When
	_, err := New(config)

	// Then
	if err == nil {
		t.Fatal("New() error = nil, want invalid config error")
	}
}

func TestClient_Resolve_rejects_invalid_request(t *testing.T) {
	// Given
	client := newHelperClient(t, 1024)

	// When
	_, err := client.Resolve(context.Background(), Request{})

	// Then
	if err == nil {
		t.Fatal("Resolve() error = nil, want invalid request error")
	}
}

func newHelperClient(t *testing.T, maxOutputBytes int64) *Client {
	t.Helper()
	client, err := New(Config{Executable: os.Args[0], MaxOutputBytes: maxOutputBytes})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return client
}

func validRequest() Request {
	return Request{IndexPath: "/tmp/index", Target: "x86_64-unknown-linux-gnu", Roots: []string{"velocity"}}
}
