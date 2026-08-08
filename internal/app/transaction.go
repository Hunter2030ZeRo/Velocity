package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Hunter2030ZeRo/velocity/internal/download"
	"github.com/Hunter2030ZeRo/velocity/internal/extract"
	"github.com/Hunter2030ZeRo/velocity/internal/install"
	"github.com/Hunter2030ZeRo/velocity/internal/registry"
	"github.com/Hunter2030ZeRo/velocity/internal/resolver"
)

type transaction struct {
	committer *install.Committer
	options   Options
	jobs      int
}

type downloadedResolution struct {
	artifacts map[string]string
	commit    string
	plan      resolver.Plan
}

func newTransaction(ctx context.Context, options Options) (transaction, error) {
	if ctx == nil {
		return transaction{}, errors.New("install application: nil context")
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return transaction{}, fmt.Errorf("install application cancelled: %w", contextErr)
	}
	jobs := options.Jobs
	if jobs == 0 {
		jobs = defaultJobs
	}
	if jobs < 1 || jobs > maxJobs {
		return transaction{}, fmt.Errorf("jobs must be between 1 and %d", maxJobs)
	}
	committer, err := install.New(install.Config{Root: options.Root})
	if err != nil {
		return transaction{}, fmt.Errorf("configure committer: %w", err)
	}
	return transaction{options: options, committer: committer, jobs: jobs}, nil
}

func (transaction transaction) resolve(ctx context.Context) (downloadedResolution, error) {
	registryClient, err := registry.New(registry.Config{
		MetadataURL: transaction.options.RegistryURL,
		CacheDir:    transaction.options.CacheDir,
		Client:      transaction.options.HTTPClient,
		AllowHTTP:   transaction.options.AllowHTTP,
	})
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("configure registry: %w", err)
	}
	index, err := registryClient.FetchIndex(ctx)
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("fetch verified registry index: %w", err)
	}
	resolverClient, err := resolver.New(resolver.Config{
		Executable:     transaction.options.ResolverExecutable,
		MaxOutputBytes: maxResolverBytes,
	})
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("configure resolver: %w", err)
	}
	plan, err := resolverClient.Resolve(ctx, resolver.Request{
		IndexPath: index.Path,
		Target:    transaction.options.Target.String(),
		Roots:     append([]string(nil), transaction.options.Roots...),
	})
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("resolve packages: %w", err)
	}
	return downloadedResolution{commit: index.Commit, plan: plan}, nil
}

func (transaction transaction) download(
	ctx context.Context,
	resolved downloadedResolution,
) (downloadedResolution, error) {
	fetcher, err := download.New(download.Config{
		CacheDir:      transaction.options.CacheDir,
		Concurrency:   transaction.jobs,
		MaxBytes:      maxArtifactBytes,
		MaxTotalBytes: maxDownloadBytes,
		MaxArtifacts:  maxDownloadArtifacts,
		Client:        transaction.options.HTTPClient,
		Progress:      transaction.options.Progress,
		AllowHTTP:     transaction.options.AllowHTTP,
	})
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("configure artifact downloader: %w", err)
	}
	artifacts := make([]download.Artifact, len(resolved.plan.Packages))
	for index, packageValue := range resolved.plan.Packages {
		artifacts[index] = download.Artifact{
			URL:    packageValue.Artifact.URL,
			SHA256: packageValue.Artifact.SHA256,
		}
	}
	paths, err := fetcher.FetchAll(ctx, artifacts)
	if err != nil {
		return downloadedResolution{}, fmt.Errorf("download verified artifacts: %w", err)
	}
	resolved.artifacts = paths
	return resolved, nil
}

func (transaction transaction) createStage() (string, error) {
	stagingRoot := filepath.Join(transaction.options.Root, ".velocity", "staging")
	if err := os.MkdirAll(stagingRoot, 0o750); err != nil {
		return "", fmt.Errorf("create staging root: %w", err)
	}
	stageDir, err := os.MkdirTemp(stagingRoot, "install-")
	if err != nil {
		return "", fmt.Errorf("create installation stage: %w", err)
	}
	return stageDir, nil
}

func (transaction transaction) extract(
	ctx context.Context,
	stageDir string,
	resolved downloadedResolution,
) ([]install.StagedBinary, error) {
	extractor, err := extract.New(extract.Config{
		Concurrency:    transaction.jobs,
		MaxBinaryBytes: maxBinaryBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("configure extractor: %w", err)
	}
	extractionJobs := make([]extract.Job, len(resolved.plan.Packages))
	for index, packageValue := range resolved.plan.Packages {
		artifact := packageValue.Artifact
		binaries := make([]extract.Binary, len(artifact.Binaries))
		for binaryIndex, binary := range artifact.Binaries {
			binaries[binaryIndex] = extract.Binary{Source: binary.Source, Name: binary.Name}
		}
		extractionJobs[index] = extract.Job{
			ArchivePath:     resolved.artifacts[strings.ToLower(artifact.SHA256)],
			SourceURL:       artifact.URL,
			Archive:         artifact.Archive,
			StripComponents: artifact.StripComponents,
			Binaries:        binaries,
		}
	}
	extracted, err := extractor.ExtractAll(ctx, stageDir, extractionJobs)
	if err != nil {
		return nil, fmt.Errorf("extract mapped binaries: %w", err)
	}
	staged := make([]install.StagedBinary, len(extracted))
	for index, binary := range extracted {
		staged[index] = install.StagedBinary{Name: binary.Name, Path: binary.Path}
	}
	return staged, nil
}
