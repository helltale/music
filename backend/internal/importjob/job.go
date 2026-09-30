// Package importjob is the PostgreSQL queue for artist imports.
package importjob

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	TypeArtistImport = "artist_import"

	StatusQueued             = "QUEUED"
	StatusRunning            = "RUNNING"
	StatusCompleted          = "COMPLETED"
	StatusPartiallyCompleted = "PARTIALLY_COMPLETED"
	StatusFailed             = "FAILED"
)

var (
	// ErrNotFound means the job id does not exist.
	ErrNotFound = errors.New("not found")
	// ErrNotRetryable means the job is not FAILED or PARTIALLY_COMPLETED.
	ErrNotRetryable = errors.New("job not retryable")
	// ErrLeaseLost means this worker no longer owns the job.
	ErrLeaseLost = errors.New("lease lost")
)

// Job is one import_jobs row.
type Job struct {
	ID            string
	Type          string
	Status        string
	Provider      string
	ExternalID    string
	Progress      json.RawMessage
	Attempt       int
	MaxAttempts   int
	NextAttemptAt time.Time
	LockedAt      *time.Time
	LockedBy      *string
	LastError     *string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
}

// EnqueueResult distinguishes a new job from the one already active for the subject.
type EnqueueResult struct {
	Job     Job
	Created bool
}
