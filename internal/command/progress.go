package command

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/term"

	"github.com/Hunter2030ZeRo/velocity/internal/download"
)

const (
	progressBarWidth       = 24
	progressByteStep int64 = 1 << 20
)

type progressMode uint8

const (
	progressAuto progressMode = iota
	progressAlways
	progressNever
)

type downloadProgress struct {
	writer     io.Writer
	mu         sync.Mutex
	last       download.Progress
	lastBucket int64
	err        error
	drawn      bool
	lineOpen   bool
}

type fileDescriptor interface {
	Fd() uintptr
}

func parseProgressMode(raw string) (progressMode, error) {
	switch raw {
	case "auto":
		return progressAuto, nil
	case "always":
		return progressAlways, nil
	case "never":
		return progressNever, nil
	default:
		return progressAuto, fmt.Errorf("progress mode %q must be auto, always, or never", raw)
	}
}

func progressEnabled(mode progressMode, writer io.Writer) bool {
	return progressEnabledWith(mode, writer, term.IsTerminal)
}

func progressEnabledWith(mode progressMode, writer io.Writer, detect func(int) bool) bool {
	switch mode {
	case progressAuto:
		descriptor, ok := writer.(fileDescriptor)
		if !ok {
			return false
		}
		return detect(int(descriptor.Fd()))
	case progressAlways:
		return true
	case progressNever:
		return false
	default:
		return false
	}
}

func newDownloadProgress(writer io.Writer) *downloadProgress {
	return &downloadProgress{writer: writer}
}

func (renderer *downloadProgress) Report(progress download.Progress) {
	if renderer == nil || progress.TotalArtifacts == 0 {
		return
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.err != nil || !renderer.shouldDraw(progress) {
		return
	}

	bar := renderProgressBar(progress.Percent)
	percent := fmt.Sprintf("%3d%%", progress.Percent)
	if progress.Indeterminate {
		bar = renderIndeterminateBar(progress.DownloadedBytes)
		percent = " ?%"
	}
	line := fmt.Sprintf(
		"\rDownloading [%s] %s  %d/%d artifacts  %s",
		bar,
		percent,
		progress.CompletedArtifacts,
		progress.TotalArtifacts,
		formatBytes(progress.DownloadedBytes),
	)
	complete := progress.TotalArtifacts > 0 && progress.CompletedArtifacts == progress.TotalArtifacts
	if complete {
		line += "\n"
	}
	if _, err := fmt.Fprint(renderer.writer, line); err != nil {
		renderer.err = fmt.Errorf("render download progress: %w", err)
		return
	}
	renderer.last = progress
	renderer.lastBucket = progress.DownloadedBytes / progressByteStep
	renderer.drawn = true
	renderer.lineOpen = !complete
}

func (renderer *downloadProgress) shouldDraw(progress download.Progress) bool {
	if !renderer.drawn {
		return true
	}
	return progress.Percent != renderer.last.Percent ||
		progress.CompletedArtifacts != renderer.last.CompletedArtifacts ||
		progress.Indeterminate != renderer.last.Indeterminate ||
		progress.DownloadedBytes/progressByteStep != renderer.lastBucket
}

func (renderer *downloadProgress) Close() error {
	if renderer == nil {
		return nil
	}
	renderer.mu.Lock()
	defer renderer.mu.Unlock()
	if renderer.lineOpen {
		if _, err := fmt.Fprintln(renderer.writer); err != nil {
			renderer.err = errors.Join(renderer.err, fmt.Errorf("finish download progress: %w", err))
		}
		renderer.lineOpen = false
	}
	return renderer.err
}

func renderIndeterminateBar(bytes int64) string {
	position := int(bytes/progressByteStep) % progressBarWidth
	return strings.Repeat(".", position) + ">" + strings.Repeat(".", progressBarWidth-position-1)
}

func renderProgressBar(percent int) string {
	bounded := min(max(percent, 0), 100)
	filled := bounded * progressBarWidth / 100
	if filled == progressBarWidth {
		return strings.Repeat("=", progressBarWidth)
	}
	return strings.Repeat("=", filled) + ">" + strings.Repeat(".", progressBarWidth-filled-1)
}

func formatBytes(bytes int64) string {
	const (
		kib float64 = 1 << 10
		mib         = 1 << 20
		gib         = 1 << 30
	)
	value := float64(bytes)
	switch {
	case value >= gib:
		return fmt.Sprintf("%.1f GiB", value/gib)
	case value >= mib:
		return fmt.Sprintf("%.1f MiB", value/mib)
	case value >= kib:
		return fmt.Sprintf("%.1f KiB", value/kib)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
