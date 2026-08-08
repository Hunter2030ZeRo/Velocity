package extract

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// defaultMaxArchiveBytes bounds expanded TAR data when callers omit an override.
const defaultMaxArchiveBytes int64 = 1 << 30

var (
	// ErrInvalidConfig reports invalid extractor configuration.
	ErrInvalidConfig = errors.New("extract: invalid config")
	// ErrInvalidJob reports invalid extraction input.
	ErrInvalidJob = errors.New("extract: invalid job")
	// ErrUnsafeArchive reports an archive entry that is unsafe to process.
	ErrUnsafeArchive = errors.New("extract: unsafe archive")
	// ErrMissingBinary reports a requested binary that is absent from an archive.
	ErrMissingBinary = errors.New("extract: missing binary")
	// ErrDuplicateSource reports a duplicated binary source in an archive or job.
	ErrDuplicateSource = errors.New("extract: duplicate source")
	// ErrDestinationExists reports an existing destination binary.
	ErrDestinationExists = errors.New("extract: destination exists")
	// ErrBinaryTooLarge reports a binary that exceeds the configured size limit.
	ErrBinaryTooLarge = errors.New("extract: binary exceeds size limit")
	// ErrArchiveTooLarge reports an archive that exceeds its work or logical-output budget.
	ErrArchiveTooLarge = errors.New("extract: archive exceeds expanded size limit")
	// ErrUnsupportedArchive reports an archive type that extraction cannot process.
	ErrUnsupportedArchive = errors.New("extract: unsupported archive")
)

// Binary maps an archive source path to its destination file name.
type Binary struct {
	Source string
	Name   string
}

// Job describes one archive and the binaries to materialize from it.
type Job struct {
	ArchivePath     string
	SourceURL       string
	Archive         string
	Binaries        []Binary
	StripComponents uint32
}

// Result identifies one materialized binary.
type Result struct {
	Name string
	Path string
}

// Config controls extraction concurrency and size limits.
type Config struct {
	Concurrency     int
	MaxBinaryBytes  int64
	MaxArchiveBytes int64
}

// Extractor safely materializes binary mappings from supported archives.
type Extractor struct {
	concurrency     int
	maxBinaryBytes  int64
	maxArchiveBytes int64
}

type jobPlan struct {
	bySource map[string]binaryPlan
	binaries []binaryPlan
	job      Job
}

type binaryPlan struct {
	source    string
	name      string
	tempPath  string
	finalPath string
	index     int
}

// New validates config and constructs an Extractor.
func New(config Config) (*Extractor, error) {
	if config.Concurrency < 1 || config.MaxBinaryBytes < 1 ||
		config.MaxArchiveBytes < 0 || config.MaxArchiveBytes == math.MaxInt64 {
		return nil, fmt.Errorf(
			"concurrency=%d max_binary_bytes=%d max_archive_bytes=%d: %w",
			config.Concurrency,
			config.MaxBinaryBytes,
			config.MaxArchiveBytes,
			ErrInvalidConfig,
		)
	}
	maxArchiveBytes := config.MaxArchiveBytes
	if maxArchiveBytes == 0 {
		maxArchiveBytes = defaultMaxArchiveBytes
	}
	return &Extractor{
		concurrency:     config.Concurrency,
		maxBinaryBytes:  config.MaxBinaryBytes,
		maxArchiveBytes: maxArchiveBytes,
	}, nil
}

func validateJobs(stageDir, workDir string, jobs []Job) ([]jobPlan, error) {
	plans := make([]jobPlan, len(jobs))
	names := make(map[string]struct{})
	resultIndex := 0
	for jobIndex, job := range jobs {
		if err := validateJob(job); err != nil {
			return nil, fmt.Errorf("job %d: %w", jobIndex, err)
		}
		plans[jobIndex] = jobPlan{job: job, bySource: make(map[string]binaryPlan)}
		sources := make(map[string]struct{})
		for _, binary := range job.Binaries {
			if !normalizedRelative(binary.Source) {
				return nil, fmt.Errorf("source %q: %w", binary.Source, ErrInvalidJob)
			}
			if !exposedBasename(binary.Name) {
				return nil, fmt.Errorf("name %q: %w", binary.Name, ErrInvalidJob)
			}
			if _, exists := names[binary.Name]; exists {
				return nil, fmt.Errorf("name %q: %w", binary.Name, ErrDestinationExists)
			}
			if _, exists := sources[binary.Source]; exists {
				return nil, fmt.Errorf("source %q: %w", binary.Source, ErrDuplicateSource)
			}
			names[binary.Name] = struct{}{}
			sources[binary.Source] = struct{}{}
			plan := binaryPlan{
				source: binary.Source, name: binary.Name, tempPath: filepath.Join(workDir, binary.Name),
				finalPath: filepath.Join(stageDir, binary.Name), index: resultIndex,
			}
			plans[jobIndex].binaries = append(plans[jobIndex].binaries, plan)
			plans[jobIndex].bySource[binary.Source] = plan
			resultIndex++
		}
	}
	return plans, nil
}

func exposedBasename(name string) bool {
	if name == "" || name == "." || name == ".." || path.IsAbs(name) || strings.Contains(name, "\\") {
		return false
	}
	return filepath.Base(name) == name && path.Base(name) == name
}

func validateJob(job Job) error {
	if job.ArchivePath == "" || len(job.Binaries) == 0 {
		return ErrInvalidJob
	}
	switch job.Archive {
	case "zip", "tar.gz", "tar.zst":
		return nil
	case "raw":
		if job.StripComponents != 0 || len(job.Binaries) != 1 {
			return ErrInvalidJob
		}
		parsed, err := url.Parse(job.SourceURL)
		if err != nil || parsed.Path == "" || path.Base(parsed.Path) != job.Binaries[0].Source {
			return ErrInvalidJob
		}
		return nil
	default:
		return fmt.Errorf("%q: %w", job.Archive, ErrUnsupportedArchive)
	}
}

func normalizedRelative(name string) bool {
	if name == "" || name == "." || path.IsAbs(name) || path.Clean(name) != name || strings.Contains(name, "\\") {
		return false
	}
	for _, component := range strings.Split(name, "/") {
		if component == ".." || component == "." {
			return false
		}
	}
	return true
}

func safeArchivePath(name string) bool {
	trimmed := strings.TrimSuffix(name, "/")
	return normalizedRelative(trimmed)
}

func strippedPath(name string, components uint32) (string, bool) {
	trimmed := strings.TrimSuffix(name, "/")
	parts := strings.Split(trimmed, "/")
	if uint64(components) >= uint64(len(parts)) {
		return "", false
	}
	return strings.Join(parts[components:], "/"), true
}
