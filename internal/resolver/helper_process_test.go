package resolver

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
)

func runHelperProcess() {
	request, err := readHelperRequest()
	if err != nil {
		writeHelperOutput(os.Stderr, "decode request: "+err.Error())
		os.Exit(2)
	}
	if validationErr := validateHelperRequest(request); validationErr != nil {
		writeHelperOutput(os.Stderr, validationErr.Error())
		os.Exit(2)
	}
	writeHelperResponse(os.Getenv("VELOCITY_RESOLVER_HELPER_MODE"))
	os.Exit(0)
}

func readHelperRequest() (sidecarRequest, error) {
	var request sidecarRequest
	err := json.NewDecoder(os.Stdin).Decode(&request)
	return request, err
}

func validateHelperRequest(request sidecarRequest) error {
	if request.Protocol != 1 || request.IndexPath == "" || request.Target == "" || len(request.Roots) == 0 {
		return errors.New("unexpected request")
	}
	return nil
}

func writeHelperResponse(mode string) {
	switch mode {
	case "ok":
		writeHelperOutput(os.Stdout, `{"protocol":1,"status":"ok","packages":[{"id":1,"name":"velocity","version":"1.0.0","artifact":{"target":"x86_64-unknown-linux-gnu","url":"https://example.invalid/velocity.tar.zst","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","archive":"tar.zst","strip_components":1,"binaries":[{"source":"bin/velocity","name":"velocity"}]}}]}`)
	case "mismatched-target":
		writeHelperOutput(os.Stdout, `{"protocol":1,"status":"ok","packages":[{"id":1,"name":"velocity","version":"1.0.0","artifact":{"target":"aarch64-unknown-linux-gnu","url":"https://example.invalid/velocity.tar.zst","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","archive":"tar.zst","strip_components":1,"binaries":[{"source":"bin/velocity","name":"velocity"}]}}]}`)
	case "printable":
		response := validSidecarResponse()
		response.Packages[0].Name = "velocity-tools"
		response.Packages[0].Version = "1.2.3-alpha.1+build.7"
		response.Packages[0].Artifact.Binaries[0] = sidecarBinary{Source: "bin/tools/velocity", Name: "velocity"}
		writeHelperPlan(os.Stdout, response)
	case "resolution-error":
		writeHelperOutput(os.Stdout, `{"protocol":1,"status":"error","error":{"code":"package_not_found","message":"velocity was not found"}}`)
	case "malformed":
		writeHelperOutput(os.Stdout, `{"protocol":1,"status":"ok"}`)
	case "oversized":
		writeHelperOutput(os.Stdout, strings.Repeat("x", 128))
	case "nonzero":
		writeHelperOutput(os.Stderr, "intentional failure")
		os.Exit(17)
	case "block":
		time.Sleep(10 * time.Second)
	case "binary-source-parent", "binary-source-backslash", "binary-name-parent", "binary-name-backslash":
		response := validSidecarResponse()
		setUnsafeBinaryPath(&response, mode)
		writeHelperPlan(os.Stdout, response)
	default:
		writeUnsafeHelperResponse(mode)
	}
}

func writeUnsafeHelperResponse(mode string) {
	switch {
	case strings.HasSuffix(mode, "-invalid-utf8"):
		writeHelperInvalidUTF8Plan(os.Stdout, mode)
	case strings.HasSuffix(mode, "-c0"), strings.HasSuffix(mode, "-c1"),
		strings.HasSuffix(mode, "-escape"), strings.HasSuffix(mode, "-del"):
		response := validSidecarResponse()
		setUnsafeMetadata(&response, mode)
		writeHelperPlan(os.Stdout, response)
	default:
		writeHelperOutput(os.Stderr, "unknown helper mode")
		os.Exit(2)
	}
}

func writeHelperOutput(writer io.StringWriter, value string) {
	if _, err := writer.WriteString(value); err != nil {
		os.Exit(2)
	}
}
