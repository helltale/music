package importjob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helltale/music/backend/internal/platform"
)

type db interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// Store reads and writes import_jobs.
type Store struct {
	db db
}

// NewStore uses a pool or a transaction.
func NewStore(db db) *Store {
	return &Store{db: db}
}

const jobColumns = `
	id, type, status, provider, subject_external_id, progress,
	attempt, max_attempts, next_attempt_at, locked_at, locked_by, last_error,
	created_at, started_at, finished_at
`

// Enqueue inserts a QUEUED artist import, or returns the active job for the same subject.
func (s *Store) Enqueue(ctx context.Context, provider, externalID string) (EnqueueResult, error) {
	id, err := platform.NewUUID()
	if err != nil {
		return EnqueueResult{}, err
	}
	job, err := s.insert(ctx, id, provider, externalID)
	if err == nil {
		return EnqueueResult{Job: job, Created: true}, nil
	}
	if !uniqueViolation(err) {
		return EnqueueResult{}, err
	}
	existing, findErr := s.active(ctx, provider, externalID)
	if errors.Is(findErr, ErrNotFound) {
		return EnqueueResult{}, err
	}
	if findErr != nil {
		return EnqueueResult{}, findErr
	}
	return EnqueueResult{Job: existing, Created: false}, nil
}

func (s *Store) insert(ctx context.Context, id, provider, externalID string) (Job, error) {
	row := s.db.QueryRow(ctx, `
		INSERT INTO import_jobs (id, type, status, provider, subject_external_id)
		VALUES ($1, $2, 'QUEUED', $3, $4)
		RETURNING `+jobColumns, id, TypeArtistImport, provider, externalID)
	job, err := scanJob(row)
	if err != nil {
		return Job{}, fmt.Errorf("enqueue import job: %w", err)
	}
	return job, nil
}

func (s *Store) active(ctx context.Context, provider, externalID string) (Job, error) {
	row := s.db.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM import_jobs
		WHERE type = $1 AND provider = $2 AND subject_external_id = $3
		  AND status IN ('QUEUED', 'RUNNING')
	`, TypeArtistImport, provider, externalID)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("find active import job: %w", err)
	}
	return job, nil
}

// Get loads one job.
func (s *Store) Get(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRow(ctx, `SELECT `+jobColumns+` FROM import_jobs WHERE id = $1`, id)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get import job: %w", err)
	}
	return job, nil
}

// List returns jobs newest first. An empty status returns every status.
func (s *Store) List(ctx context.Context, status string, limit, offset int) ([]Job, int, error) {
	filter := status != ""
	var total int
	if err := s.db.QueryRow(ctx, `
		SELECT count(*) FROM import_jobs WHERE NOT $1 OR status = $2
	`, filter, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count import jobs: %w", err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+jobColumns+`
		FROM import_jobs
		WHERE NOT $1 OR status = $2
		ORDER BY created_at DESC, id DESC
		LIMIT $3 OFFSET $4
	`, filter, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list import jobs: %w", err)
	}
	defer rows.Close()
	var items []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan import job: %w", err)
		}
		items = append(items, job)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("list import jobs: %w", err)
	}
	if items == nil {
		items = []Job{}
	}
	return items, total, nil
}

// Claim takes the oldest due job. ok is false when the queue has nothing available.
func (s *Store) Claim(ctx context.Context, workerID string) (Job, bool, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE import_jobs
		SET status = 'RUNNING',
		    locked_at = now(),
		    locked_by = $1,
		    started_at = now(),
		    attempt = attempt + 1
		WHERE id = (
		    SELECT id
		    FROM import_jobs
		    WHERE status = 'QUEUED'
		      AND next_attempt_at <= now()
		      AND attempt < max_attempts
		    ORDER BY next_attempt_at, created_at
		    FOR UPDATE SKIP LOCKED
		    LIMIT 1
		)
		RETURNING `+jobColumns, workerID)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, fmt.Errorf("claim import job: %w", err)
	}
	return job, true, nil
}

// Heartbeat extends the lease. A false result means the worker lost the job.
func (s *Store) Heartbeat(ctx context.Context, id, workerID string, attempt int) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE import_jobs
		SET locked_at = now()
		WHERE id = $1 AND locked_by = $2 AND attempt = $3 AND status = 'RUNNING'
	`, id, workerID, attempt)
	if err != nil {
		return false, fmt.Errorf("heartbeat import job: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// SaveProgress writes worker progress while the lease is still held.
func (s *Store) SaveProgress(ctx context.Context, id, workerID string, attempt int, progress []byte) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE import_jobs
		SET progress = $4::jsonb
		WHERE id = $1 AND locked_by = $2 AND attempt = $3 AND status = 'RUNNING'
	`, id, workerID, attempt, string(progress))
	if err != nil {
		return fmt.Errorf("save import progress: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Finish marks a terminal status and releases the lock.
func (s *Store) Finish(ctx context.Context, id, workerID string, attempt int, status, lastError string, progress []byte) error {
	var errorValue any
	if lastError != "" {
		errorValue = lastError
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE import_jobs
		SET status = $4,
		    progress = $5::jsonb,
		    last_error = $6,
		    locked_at = NULL,
		    locked_by = NULL,
		    finished_at = now()
		WHERE id = $1 AND locked_by = $2 AND attempt = $3 AND status = 'RUNNING'
	`, id, workerID, attempt, status, string(progress), errorValue)
	if err != nil {
		return fmt.Errorf("finish import job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Requeue returns a failed attempt to the queue, or fails it when attempts are exhausted.
// The returned status is QUEUED or FAILED.
func (s *Store) Requeue(ctx context.Context, id, workerID string, attempt int, lastError string) (string, error) {
	var status string
	err := s.db.QueryRow(ctx, `
		UPDATE import_jobs
		SET status = CASE WHEN attempt >= max_attempts THEN 'FAILED' ELSE 'QUEUED' END,
		    locked_at = NULL,
		    locked_by = NULL,
		    last_error = $4,
		    finished_at = CASE WHEN attempt >= max_attempts THEN now() ELSE NULL END,
		    next_attempt_at = CASE
		        WHEN attempt >= max_attempts THEN next_attempt_at
		        ELSE now() + least(power(2, attempt) * interval '1 second', interval '5 minutes')
		    END
		WHERE id = $1 AND locked_by = $2 AND attempt = $3 AND status = 'RUNNING'
		RETURNING status
	`, id, workerID, attempt, lastError).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrLeaseLost
	}
	if err != nil {
		return "", fmt.Errorf("requeue import job: %w", err)
	}
	return status, nil
}

// Release returns a job to the queue during shutdown and does not count the attempt.
func (s *Store) Release(ctx context.Context, id, workerID string, attempt int) error {
	_, err := s.db.Exec(ctx, `
		UPDATE import_jobs
		SET status = 'QUEUED',
		    attempt = attempt - 1,
		    locked_at = NULL,
		    locked_by = NULL,
		    started_at = NULL,
		    next_attempt_at = now()
		WHERE id = $1 AND locked_by = $2 AND attempt = $3 AND status = 'RUNNING' AND attempt > 0
	`, id, workerID, attempt)
	if err != nil {
		return fmt.Errorf("release import job: %w", err)
	}
	return nil
}

// Recover returns expired RUNNING jobs to the queue. Attempt is left as it was at claim.
func (s *Store) Recover(ctx context.Context, lease time.Duration) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE import_jobs
		SET status = CASE
		        WHEN attempt >= max_attempts THEN 'FAILED'
		        ELSE 'QUEUED'
		    END,
		    locked_at = NULL,
		    locked_by = NULL,
		    next_attempt_at = now(),
		    last_error = COALESCE(last_error, 'lease expired'),
		    finished_at = CASE
		        WHEN attempt >= max_attempts THEN now()
		        ELSE NULL
		    END
		WHERE status = 'RUNNING'
		  AND locked_at < now() - ($1 * interval '1 millisecond')
	`, lease.Milliseconds())
	if err != nil {
		return 0, fmt.Errorf("recover import jobs: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Retry puts a finished unsuccessful job back at the start of the queue.
func (s *Store) Retry(ctx context.Context, id string) (Job, error) {
	row := s.db.QueryRow(ctx, `
		UPDATE import_jobs
		SET status = 'QUEUED',
		    attempt = 0,
		    locked_at = NULL,
		    locked_by = NULL,
		    last_error = NULL,
		    finished_at = NULL,
		    next_attempt_at = now()
		WHERE id = $1 AND status IN ('FAILED', 'PARTIALLY_COMPLETED')
		RETURNING `+jobColumns, id)
	job, err := scanJob(row)
	if err == nil {
		return job, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Job{}, fmt.Errorf("retry import job: %w", err)
	}
	if _, getErr := s.Get(ctx, id); errors.Is(getErr, ErrNotFound) {
		return Job{}, ErrNotFound
	} else if getErr != nil {
		return Job{}, getErr
	}
	return Job{}, ErrNotRetryable
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(row jobScanner) (Job, error) {
	var job Job
	var progress []byte
	err := row.Scan(
		&job.ID, &job.Type, &job.Status, &job.Provider, &job.ExternalID, &progress,
		&job.Attempt, &job.MaxAttempts, &job.NextAttemptAt, &job.LockedAt, &job.LockedBy, &job.LastError,
		&job.CreatedAt, &job.StartedAt, &job.FinishedAt,
	)
	if err != nil {
		return Job{}, err
	}
	if len(progress) == 0 {
		progress = []byte(`{}`)
	}
	job.Progress = progress
	return job, nil
}

func uniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
