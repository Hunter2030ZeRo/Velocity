package resolver

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

type sidecarResponse struct {
	Error    *sidecarErrorBody `json:"error"`
	Status   string            `json:"status"`
	Packages []sidecarPackage  `json:"packages"`
	Protocol uint32            `json:"protocol"`
}

type sidecarPackage struct {
	Name     string          `json:"name"`
	Version  string          `json:"version"`
	Artifact sidecarArtifact `json:"artifact"`
	ID       uint64          `json:"id"`
}

type sidecarArtifact struct {
	Target          string          `json:"target"`
	URL             string          `json:"url"`
	SHA256          string          `json:"sha256"`
	Archive         string          `json:"archive"`
	Binaries        []sidecarBinary `json:"binaries"`
	StripComponents uint32          `json:"strip_components"`
}

type sidecarBinary struct {
	Source string `json:"source"`
	Name   string `json:"name"`
}

type sidecarErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodePlan(output []byte, expectedTarget string) (Plan, error) {
	if !utf8.Valid(output) {
		return Plan{}, errors.New("decode response: invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	var response sidecarResponse
	if err := decoder.Decode(&response); err != nil {
		return Plan{}, fmt.Errorf("decode response: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Plan{}, errors.New("decode response: trailing JSON value")
		}
		return Plan{}, fmt.Errorf("decode response: trailing data: %w", err)
	}
	if response.Protocol != protocolVersion {
		return Plan{}, fmt.Errorf("decode response: unsupported protocol %d", response.Protocol)
	}

	switch response.Status {
	case "ok":
		if response.Error != nil || response.Packages == nil {
			return Plan{}, errors.New("decode response: invalid ok response")
		}
		return planFromSidecar(response.Packages, expectedTarget)
	case "error":
		if response.Error == nil || response.Packages != nil ||
			!isNonEmpty(response.Error.Code) || !isNonEmpty(response.Error.Message) {
			return Plan{}, errors.New("decode response: invalid error response")
		}
		return Plan{}, &ResolutionError{Code: response.Error.Code, Message: response.Error.Message}
	default:
		return Plan{}, fmt.Errorf("decode response: unsupported status %q", response.Status)
	}
}

func planFromSidecar(packages []sidecarPackage, expectedTarget string) (Plan, error) {
	plan := Plan{Packages: make([]Package, len(packages))}
	for index, sidecarPackage := range packages {
		artifact := Artifact{
			Target:          sidecarPackage.Artifact.Target,
			URL:             sidecarPackage.Artifact.URL,
			SHA256:          sidecarPackage.Artifact.SHA256,
			Archive:         sidecarPackage.Artifact.Archive,
			StripComponents: sidecarPackage.Artifact.StripComponents,
			Binaries:        make([]Binary, len(sidecarPackage.Artifact.Binaries)),
		}
		for binaryIndex, binary := range sidecarPackage.Artifact.Binaries {
			artifact.Binaries[binaryIndex] = Binary(binary)
		}
		packageValue := Package{
			Name: sidecarPackage.Name, Version: sidecarPackage.Version,
			Artifact: artifact, ID: sidecarPackage.ID,
		}
		if err := validatePackage(packageValue, expectedTarget); err != nil {
			return Plan{}, fmt.Errorf("decode response: package %d: %w", index, err)
		}
		plan.Packages[index] = packageValue
	}
	return plan, nil
}

func validateRequest(request Request) error {
	if !isNonEmpty(request.IndexPath) || !isNonEmpty(request.Target) || len(request.Roots) == 0 {
		return errors.New("resolver request requires index path, target, and roots")
	}
	for _, root := range request.Roots {
		if !isNonEmpty(root) {
			return errors.New("resolver request root must not be empty")
		}
	}
	return nil
}

func validatePackage(packageValue Package, expectedTarget string) error {
	if packageValue.ID == 0 || !isSafeText(packageValue.Name) || !isSafeText(packageValue.Version) {
		return errors.New("invalid package identity")
	}
	return validateArtifact(packageValue.Artifact, expectedTarget)
}

func validateArtifact(artifact Artifact, expectedTarget string) error {
	if !isNonEmpty(artifact.Target) || !isNonEmpty(artifact.Archive) || len(artifact.Binaries) == 0 {
		return errors.New("invalid artifact metadata")
	}
	if artifact.Target != expectedTarget {
		return fmt.Errorf("artifact target %q does not match request target %q", artifact.Target, expectedTarget)
	}
	parsedURL, err := url.Parse(artifact.URL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		return errors.New("invalid artifact URL")
	}
	if len(artifact.SHA256) != 64 {
		return errors.New("invalid artifact SHA-256")
	}
	if _, decodeErr := hex.DecodeString(artifact.SHA256); decodeErr != nil {
		return fmt.Errorf("invalid artifact SHA-256: %w", decodeErr)
	}
	for _, binary := range artifact.Binaries {
		if !isSafeRelativePath(binary.Source) || !isSafeBinaryName(binary.Name) {
			return errors.New("invalid artifact binary")
		}
	}
	return nil
}

func isNonEmpty(value string) bool {
	return strings.TrimSpace(value) != ""
}

func isSafeText(value string) bool {
	if !isNonEmpty(value) || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || (character >= 0x7f && character <= 0x9f) || !unicode.IsPrint(character) {
			return false
		}
	}
	return true
}

func isSafeRelativePath(value string) bool {
	return isSafeText(value) &&
		!strings.Contains(value, "\\") && !strings.HasPrefix(value, "/") && path.Clean(value) == value &&
		value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func isSafeBinaryName(value string) bool {
	return isSafeText(value) && !strings.Contains(value, "\\") &&
		path.Base(value) == value && path.Clean(value) == value && value != "." && value != ".."
}
