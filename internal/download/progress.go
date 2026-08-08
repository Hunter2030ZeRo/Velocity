package download

import (
	"io"
	"math"
	"sync"
)

const progressScale int64 = 10_000

// BatchID identifies one FetchAll operation on a Fetcher.
type BatchID uint64

// Progress is an aggregate, monotonic snapshot of one FetchAll operation.
type Progress struct {
	BatchID            BatchID
	DownloadedBytes    int64
	TotalBytes         int64
	CompletedArtifacts int
	TotalArtifacts     int
	Percent            int
	Indeterminate      bool
}

// ProgressFunc consumes FetchAll snapshots serialized per Fetcher.
type ProgressFunc func(Progress)

type progressDispatcher struct {
	report ProgressFunc
	mu     sync.Mutex
}

type progressTracker struct {
	dispatch *progressDispatcher
	snapshot Progress
	mu       sync.Mutex
	units    int64
	unknown  int
}

type artifactProgress struct {
	tracker       *progressTracker
	total         int64
	written       int64
	accounted     int64
	units         int64
	indeterminate bool
	finished      bool
}

type progressWriter struct {
	destination io.Writer
	artifact    *artifactProgress
}

func newProgressDispatcher(report ProgressFunc) *progressDispatcher {
	if report == nil {
		return nil
	}
	return &progressDispatcher{report: report}
}

func newProgressTracker(dispatch *progressDispatcher, batchID BatchID, artifacts int) *progressTracker {
	if dispatch == nil {
		return nil
	}
	tracker := &progressTracker{
		dispatch: dispatch,
		snapshot: Progress{
			BatchID:        batchID,
			TotalArtifacts: artifacts,
		},
	}
	if artifacts == 0 {
		tracker.snapshot.Percent = 100
	}
	dispatch.send(tracker.snapshot)
	return tracker
}

func (dispatcher *progressDispatcher) send(progress Progress) {
	dispatcher.mu.Lock()
	defer dispatcher.mu.Unlock()
	dispatcher.report(progress)
}

func (tracker *progressTracker) start(total int64) *artifactProgress {
	if tracker == nil {
		return nil
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	artifact := &artifactProgress{tracker: tracker, total: total}
	if total > 0 {
		tracker.addTotal(total)
		artifact.accounted = total
	} else {
		tracker.unknown++
		tracker.snapshot.Indeterminate = true
		artifact.indeterminate = true
	}
	tracker.dispatch.send(tracker.snapshot)
	return artifact
}

func (tracker *progressTracker) completeCached() {
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	tracker.snapshot.CompletedArtifacts++
	tracker.units += progressScale
	tracker.updatePercent()
	tracker.dispatch.send(tracker.snapshot)
}

func (artifact *artifactProgress) advance(bytes int64) {
	if artifact == nil || bytes <= 0 {
		return
	}
	tracker := artifact.tracker
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if artifact.finished {
		return
	}
	artifact.written += bytes
	tracker.snapshot.DownloadedBytes += bytes
	if artifact.written > artifact.accounted {
		tracker.addTotal(artifact.written - artifact.accounted)
		artifact.accounted = artifact.written
	}
	if artifact.total > 0 && artifact.written > artifact.total && !artifact.indeterminate {
		artifact.indeterminate = true
		tracker.unknown++
		tracker.snapshot.Indeterminate = true
	}
	if artifact.total > 0 {
		fraction := float64(min(artifact.written, artifact.total)) / float64(artifact.total)
		units := min(int64(fraction*float64(progressScale)), progressScale-1)
		tracker.units += units - artifact.units
		artifact.units = units
		tracker.updatePercent()
	}
	tracker.dispatch.send(tracker.snapshot)
}

func (artifact *artifactProgress) finish(success bool) {
	if artifact == nil {
		return
	}
	tracker := artifact.tracker
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if artifact.finished {
		return
	}
	artifact.finished = true
	if artifact.indeterminate {
		tracker.unknown--
		tracker.snapshot.Indeterminate = tracker.unknown > 0
	}
	if success {
		tracker.snapshot.CompletedArtifacts++
		tracker.units += progressScale - artifact.units
		tracker.updatePercent()
	}
	tracker.dispatch.send(tracker.snapshot)
}

func (tracker *progressTracker) addTotal(bytes int64) {
	remaining := math.MaxInt64 - tracker.snapshot.TotalBytes
	tracker.snapshot.TotalBytes += min(bytes, remaining)
}

func (tracker *progressTracker) updatePercent() {
	totalUnits := int64(tracker.snapshot.TotalArtifacts) * progressScale
	if totalUnits > 0 {
		tracker.snapshot.Percent = int(min(tracker.units*100/totalUnits, 100))
	}
}

func (writer *progressWriter) Write(buffer []byte) (int, error) {
	written, err := writer.destination.Write(buffer)
	writer.artifact.advance(int64(written))
	return written, err
}
