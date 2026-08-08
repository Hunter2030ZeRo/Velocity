package app_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
)

const (
	helperEnvironment           = "VELOCITY_APP_RESOLVER_HELPER"
	helperPlanEnvironment       = "VELOCITY_APP_RESOLVER_PLAN"
	helperInvocationEnvironment = "VELOCITY_APP_RESOLVER_INVOCATION"
)

type fixtureOptions struct {
	corruptArtifact bool
}

type registryFixture struct {
	metadataURL string
	client      *http.Client
	plan        string
	wantResult  app.Result
}

func newRegistryFixture(t *testing.T, options fixtureOptions) registryFixture {
	t.Helper()
	dependency := []byte("dependency executable")
	rootArchive := tarGzip(t, "package/bin/root", []byte("root executable"))
	servedRootArchive := rootArchive
	if options.corruptArtifact {
		servedRootArchive = append(append([]byte(nil), rootArchive...), 0)
	}
	index := []byte("verified index")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/registry.json":
			if _, err := fmt.Fprintf(response, `{"format":1,"commit":"%s","index":"index.cbor","sha256":"%s"}`, commit, digest(index)); err != nil {
				return
			}
		case "/index.cbor":
			if _, err := response.Write(index); err != nil {
				return
			}
		case "/dep":
			if _, err := response.Write(dependency); err != nil {
				return
			}
		case "/root.tar.gz":
			if _, err := response.Write(servedRootArchive); err != nil {
				return
			}
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	packages := []map[string]any{
		packagePlan(packagePlanOptions{
			id: 1, name: "dependency", version: "1.0.0", url: server.URL + "/dep",
			checksum: digest(dependency), archive: "raw", source: "dep", binary: "dep",
		}),
		packagePlan(packagePlanOptions{
			id: 2, name: "root", version: "2.0.0", url: server.URL + "/root.tar.gz",
			checksum: digest(rootArchive), archive: "tar.gz", strip: 1, source: "bin/root", binary: "root",
		}),
	}
	planBytes, err := json.Marshal(map[string]any{"protocol": 1, "status": "ok", "packages": packages})
	requireNoError(t, err)
	return registryFixture{
		metadataURL: server.URL + "/registry.json", client: server.Client(), plan: string(planBytes),
		wantResult: app.Result{
			RegistryCommit: commit,
			Packages:       []app.InstalledPackage{{Name: "dependency", Version: "1.0.0"}, {Name: "root", Version: "2.0.0"}},
			Installed:      []string{},
		},
	}
}

const commit = "0123456789abcdef0123456789abcdef01234567"

type packagePlanOptions struct {
	name, version          string
	url, checksum, archive string
	source, binary         string
	id                     int
	strip                  uint32
}

func packagePlan(options packagePlanOptions) map[string]any {
	return map[string]any{
		"id": options.id, "name": options.name, "version": options.version,
		"artifact": map[string]any{
			"target": "x86_64-unknown-linux-gnu", "url": options.url, "sha256": options.checksum,
			"archive": options.archive, "strip_components": options.strip,
			"binaries": []map[string]string{{"source": options.source, "name": options.binary}},
		},
	}
}

func digest(contents []byte) string {
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func tarGzip(t *testing.T, name string, contents []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	requireNoError(t, tarWriter.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}))
	_, err := tarWriter.Write(contents)
	requireNoError(t, err)
	requireNoError(t, tarWriter.Close())
	requireNoError(t, gzipWriter.Close())
	return output.Bytes()
}

func runResolverHelper() {
	if marker := os.Getenv(helperInvocationEnvironment); marker != "" {
		if err := os.WriteFile(marker, []byte("invoked"), 0o600); err != nil {
			os.Exit(3)
		}
	}
	var request map[string]any
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	if _, err := os.Stdout.WriteString(os.Getenv(helperPlanEnvironment)); err != nil {
		os.Exit(4)
	}
	os.Exit(0)
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
