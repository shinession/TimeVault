package backup

// Phases of a backup, in order. "scanning" covers both enumerating folders and
// copying changed content, because the two are interleaved per folder.
const (
	PhasePreparing  = "snapshot_preparing"
	PhaseScanning   = "scanning"
	PhaseValidating = "manifest_validating"
	PhaseCommitting = "committing"
	PhaseCommitted  = "committed"
	PhaseVerifying  = "verifying"
)

// Phases of the maintenance that removes old history: pruning deletes versions from the catalog, and
// sweeping deletes the content that no version refers to any more from the repository.
const (
	PhasePruning  = "pruning"
	PhaseSweeping = "sweeping"
)

// Progress is a snapshot of a running backup or verification.
type Progress struct {
	Phase       string `json:"phase"`
	CurrentPath string `json:"current_path,omitempty"`
	Dirs        int64  `json:"dirs"`
	Files       int64  `json:"files"`
	Added       int64  `json:"added"`
	Modified    int64  `json:"modified"`
	Deleted     int64  `json:"deleted"`
	Skipped     int64  `json:"skipped"`
	NewObjects  int64  `json:"new_objects"`
	BytesHashed int64  `json:"bytes_hashed"`
}

// Reporter receives status from a running operation. Implementations must be
// safe for concurrent use and must not block for long.
type Reporter interface {
	Phase(name string)
	Progress(p Progress)
	// Event records a human-readable line in the job log.
	Event(level, message string)
}

// NopReporter discards everything.
type NopReporter struct{}

func (NopReporter) Phase(string)         {}
func (NopReporter) Progress(Progress)    {}
func (NopReporter) Event(string, string) {}
