package resolver

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

func validSidecarResponse() sidecarResponse {
	return sidecarResponse{
		Protocol: 1,
		Status:   "ok",
		Packages: []sidecarPackage{{
			ID: 1, Name: "velocity", Version: "1.0.0",
			Artifact: sidecarArtifact{
				Target: "x86_64-unknown-linux-gnu", URL: "https://example.invalid/velocity.tar.zst",
				SHA256: strings.Repeat("a", 64), Archive: "tar.zst", StripComponents: 1,
				Binaries: []sidecarBinary{{Source: "bin/velocity", Name: "velocity"}},
			},
		}},
	}
}

func writeHelperPlan(writer io.Writer, response sidecarResponse) {
	value, err := json.Marshal(response)
	if err != nil {
		os.Exit(2)
	}
	writeHelperBytes(writer, value)
}

func writeHelperInvalidUTF8Plan(writer io.Writer, field string) {
	response := validSidecarResponse()
	marker := "invalid-utf8-marker"
	switch {
	case strings.HasPrefix(field, "package-name-"):
		response.Packages[0].Name = marker
	case strings.HasPrefix(field, "package-version-"):
		response.Packages[0].Version = marker
	case strings.HasPrefix(field, "binary-source-"):
		response.Packages[0].Artifact.Binaries[0].Source = marker
	default:
		response.Packages[0].Artifact.Binaries[0].Name = marker
	}
	value, err := json.Marshal(response)
	if err != nil {
		os.Exit(2)
	}
	quotedMarker := []byte(`"` + marker + `"`)
	index := bytes.Index(value, quotedMarker)
	if index < 0 {
		os.Exit(2)
	}
	value = append(value[:index], append([]byte{'"', 0xff, '"'}, value[index+len(quotedMarker):]...)...)
	writeHelperBytes(writer, value)
}

func unsafeMetadataValue(mode string) string {
	switch {
	case strings.HasSuffix(mode, "-c0"):
		return "velocity\x00"
	case strings.HasSuffix(mode, "-c1"):
		return "velocity\u0085"
	case strings.HasSuffix(mode, "-escape"):
		return "velocity\x1b[31m"
	default:
		return "velocity\x7f"
	}
}

func setUnsafeMetadata(response *sidecarResponse, mode string) {
	value := unsafeMetadataValue(mode)
	switch {
	case strings.HasPrefix(mode, "package-name-"):
		response.Packages[0].Name = value
	case strings.HasPrefix(mode, "package-version-"):
		response.Packages[0].Version = value
	case strings.HasPrefix(mode, "binary-source-"):
		response.Packages[0].Artifact.Binaries[0].Source = value
	default:
		response.Packages[0].Artifact.Binaries[0].Name = value
	}
}

func setUnsafeBinaryPath(response *sidecarResponse, mode string) {
	value := ".."
	if strings.HasSuffix(mode, "backslash") {
		value = "bin\\velocity"
	}
	if strings.HasPrefix(mode, "binary-source-") {
		response.Packages[0].Artifact.Binaries[0].Source = value
		return
	}
	response.Packages[0].Artifact.Binaries[0].Name = value
}

func writeHelperBytes(writer io.Writer, value []byte) {
	if _, err := writer.Write(value); err != nil {
		os.Exit(2)
	}
}
