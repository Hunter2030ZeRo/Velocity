// Package app orchestrates complete Velocity installation transactions.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/Hunter2030ZeRo/velocity/internal/platform"
)

const (
	defaultJobs                = 4
	maxJobs                    = 64
	maxArtifactBytes     int64 = 1 << 30
	maxDownloadBytes     int64 = 1 << 30
	maxDownloadArtifacts       = 256
	maxBinaryBytes       int64 = 512 << 20
	maxResolverBytes     int64 = 16 << 20
)

// Options contains reusable installation inputs and adapter configuration.
type Options struct {
	HTTPClient         *http.Client
	Root               string
	CacheDir           string
	ResolverExecutable string
	RegistryURL        string
	Target             platform.Target
	Roots              []string
	Jobs               int
	AllowHTTP          bool
}

// InstalledPackage identifies one selected package in dependency order.
type InstalledPackage struct {
	Name    string
	Version string
}

// Result reports the verified registry revision and committed installation.
type Result struct {
	RegistryCommit string
	Packages       []InstalledPackage
	Installed      []string
}

// Install executes one verified, staged, no-clobber installation transaction.
func Install(ctx context.Context, options Options) (_ Result, err error) {
	transaction, err := newTransaction(ctx, options)
	if err != nil {
		return Result{}, err
	}
	resolved, err := transaction.resolve(ctx)
	if err != nil {
		return Result{}, err
	}
	downloaded, err := transaction.download(ctx, resolved)
	if err != nil {
		return Result{}, err
	}
	stageDir, err := transaction.createStage()
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(stageDir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove installation stage: %w", cleanupErr))
		}
	}()
	staged, err := transaction.extract(ctx, stageDir, downloaded)
	if err != nil {
		return Result{}, err
	}
	installed, err := transaction.committer.Commit(ctx, staged)
	if err != nil {
		return Result{}, fmt.Errorf("commit installation: %w", err)
	}

	packages := make([]InstalledPackage, len(downloaded.plan.Packages))
	for index, packageValue := range downloaded.plan.Packages {
		packages[index] = InstalledPackage{Name: packageValue.Name, Version: packageValue.Version}
	}
	return Result{RegistryCommit: downloaded.commit, Packages: packages, Installed: installed}, nil
}
