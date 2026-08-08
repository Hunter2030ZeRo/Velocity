// Package command provides the Velocity command-line interface.
package command

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/spf13/cobra"

	"github.com/Hunter2030ZeRo/velocity/internal/app"
	"github.com/Hunter2030ZeRo/velocity/internal/platform"
	"github.com/Hunter2030ZeRo/velocity/internal/registry"
)

const defaultJobs = 4

// InstallFunc is the narrow application boundary used by the install command.
type InstallFunc func(context.Context, app.Options) (app.Result, error)

// Execute runs the production Velocity command tree with the supplied context.
func Execute(ctx context.Context) error {
	//nolint:contextcheck // Cobra exposes ExecuteContext's context through command.Context in RunE.
	return NewRoot(app.Install).ExecuteContext(ctx)
}

// NewRoot constructs a command tree with an injected install application.
func NewRoot(install InstallFunc) *cobra.Command {
	root := &cobra.Command{
		Use:           "velocity",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInstallCommand(install))
	return root
}

func newInstallCommand(install InstallFunc) *cobra.Command {
	var rootPath string
	var cachePath string
	var resolverPath string
	var registryURL string
	var jobs int
	var targetRaw string

	command := &cobra.Command{
		Use:   "install <package>...",
		Short: "Install packages and their dependencies",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(command *cobra.Command, packages []string) error {
			if jobs <= 0 {
				return errors.New("jobs must be greater than zero")
			}
			options, err := defaultOptions()
			if err != nil {
				return err
			}
			options.Roots = append([]string(nil), packages...)
			options.Jobs = jobs
			options.RegistryURL = registryURL
			if command.Flags().Changed("root") {
				options.Root = rootPath
			}
			if command.Flags().Changed("cache") {
				options.CacheDir = cachePath
			}
			if command.Flags().Changed("resolver") {
				options.ResolverExecutable = resolverPath
			}
			if targetRaw != "" {
				options.Target, err = platform.Parse(targetRaw)
				if err != nil {
					return fmt.Errorf("parse target: %w", err)
				}
			}
			result, err := install(command.Context(), options)
			if err != nil {
				return fmt.Errorf("install packages: %w", err)
			}
			return renderResult(command, result)
		},
	}
	flags := command.Flags()
	flags.StringVar(&rootPath, "root", "", "installation root")
	flags.StringVar(&cachePath, "cache", "", "cache directory")
	flags.StringVar(&resolverPath, "resolver", "", "resolver executable")
	flags.StringVar(&registryURL, "registry", registry.DefaultMetadataURL, "registry metadata URL")
	flags.IntVar(&jobs, "jobs", defaultJobs, "parallel artifact downloads")
	flags.StringVar(&targetRaw, "target", "", "registry target triple")
	return command
}

func defaultOptions() (app.Options, error) {
	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return app.Options{}, fmt.Errorf("find user cache directory: %w", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return app.Options{}, fmt.Errorf("find user home directory: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return app.Options{}, fmt.Errorf("find velocity executable: %w", err)
	}
	target, err := platform.Detect(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return app.Options{}, fmt.Errorf("detect host target: %w", err)
	}
	return app.Options{
		Target:             target,
		Root:               defaultRoot(home),
		CacheDir:           filepath.Join(cacheBase, "velocity"),
		ResolverExecutable: filepath.Join(filepath.Dir(executable), resolverName()),
		RegistryURL:        registry.DefaultMetadataURL,
		Jobs:               defaultJobs,
	}, nil
}

func defaultRoot(home string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "velocity")
	}
	return filepath.Join(home, ".local")
}

func resolverName() string {
	if runtime.GOOS == "windows" {
		return "velocity-resolver.exe"
	}
	return "velocity-resolver"
}

func renderResult(command *cobra.Command, result app.Result) error {
	packages := append([]app.InstalledPackage(nil), result.Packages...)
	sort.Slice(packages, func(left, right int) bool {
		if packages[left].Name == packages[right].Name {
			return packages[left].Version < packages[right].Version
		}
		return packages[left].Name < packages[right].Name
	})
	installed := append([]string(nil), result.Installed...)
	sort.Strings(installed)
	if result.RegistryCommit != "" {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "registry: %s\n", result.RegistryCommit); err != nil {
			return fmt.Errorf("write registry result: %w", err)
		}
	}
	for _, packageValue := range packages {
		if _, err := fmt.Fprintf(
			command.OutOrStdout(),
			"package: %s@%s\n",
			packageValue.Name,
			packageValue.Version,
		); err != nil {
			return fmt.Errorf("write package result: %w", err)
		}
	}
	for _, path := range installed {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "installed: %s\n", path); err != nil {
			return fmt.Errorf("write installed result: %w", err)
		}
	}
	return nil
}
