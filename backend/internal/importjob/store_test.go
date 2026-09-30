package importjob

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/helltale/music/backend/internal/platform"
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	adminURL := getenv("TEST_ADMIN_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music?sslmode=disable")
	testURL := getenv("TEST_DATABASE_URL", "postgres://music:music@127.0.0.1:5432/music_importjob_test?sslmode=disable")

	admin, err := platform.OpenPostgres(ctx, adminURL)
	if err != nil {
		os.Stderr.WriteString("import job tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE music_importjob_test`); err != nil && !duplicateDatabase(err) {
		os.Stderr.WriteString("create music_importjob_test: " + err.Error() + "\n")
		os.Exit(1)
	}
	admin.Close()

	pool, err := platform.OpenPostgres(ctx, testURL)
	if err != nil {
		os.Stderr.WriteString("import job tests: " + err.Error() + "\n")
		os.Exit(1)
	}
	if _, err := platform.Apply(ctx, pool, migrationsDir()); err != nil {
		os.Stderr.WriteString("apply migrations: " + err.Error() + "\n")
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

var testPool *pgxpool.Pool

func TestEnqueueReturnsActiveJob(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testPool)
	externalID := newID(t)
	first, err := store.Enqueue(ctx, "queue-test", externalID)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Job.Status != StatusQueued || first.Job.Attempt != 0 {
		t.Fatalf("first = %+v", first)
	}
	second, err := store.Enqueue(ctx, "queue-test", externalID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.Job.ID != first.Job.ID {
		t.Fatalf("second = %+v", second)
	}
	if err := store.Finish(ctx, first.Job.ID, newID(t), 1, StatusCompleted, "", []byte(`{}`)); err != ErrLeaseLost {
		t.Fatalf("finish without lease = %v", err)
	}
}

func TestClaimIsExclusive(t *testing.T) {
	ctx := context.Background()
	pushOtherJobs(t)
	store := NewStore(testPool)
	job, err := store.Enqueue(ctx, "queue-test", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var won []Job
	var wg sync.WaitGroup
	workers := make([]string, 8)
	for i := range workers {
		workers[i] = newID(t)
	}
	for _, workerID := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, ok, err := store.Claim(ctx, workerID)
			if err != nil {
				t.Error(err)
				return
			}
			if !ok {
				return
			}
			mu.Lock()
			won = append(won, claimed)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(won) != 1 || won[0].ID != job.Job.ID || won[0].Attempt != 1 || won[0].Status != StatusRunning {
		t.Fatalf("won = %+v", won)
	}
	ok, err := store.Heartbeat(ctx, won[0].ID, *won[0].LockedBy, won[0].Attempt)
	if err != nil || !ok {
		t.Fatalf("heartbeat ok=%v err=%v", ok, err)
	}
	ok, err = store.Heartbeat(ctx, won[0].ID, newID(t), won[0].Attempt)
	if err != nil || ok {
		t.Fatalf("foreign heartbeat ok=%v err=%v", ok, err)
	}
}

func TestTwoWorkersClaimDifferentJobs(t *testing.T) {
	ctx := context.Background()
	pushOtherJobs(t)
	store := NewStore(testPool)
	first, err := store.Enqueue(ctx, "queue-test", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Enqueue(ctx, "queue-test", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	left, ok, err := store.Claim(ctx, newID(t))
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	right, ok, err := store.Claim(ctx, newID(t))
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if left.ID == right.ID {
		t.Fatal("both workers claimed one job")
	}
	got := map[string]bool{left.ID: true, right.ID: true}
	if !got[first.Job.ID] || !got[second.Job.ID] {
		t.Fatalf("claimed %s %s", left.ID, right.ID)
	}
	if _, ok, err := store.Claim(ctx, newID(t)); err != nil || ok {
		t.Fatalf("third claim ok=%v err=%v", ok, err)
	}
}

func TestReleaseAndRecover(t *testing.T) {
	ctx := context.Background()
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, "queue-test", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	workerID := newID(t)
	job, ok, err := store.Claim(ctx, workerID)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if err := store.Release(ctx, job.ID, workerID, job.Attempt); err != nil {
		t.Fatal(err)
	}
	released, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if released.Status != StatusQueued || released.Attempt != 0 || released.LockedBy != nil {
		t.Fatalf("released = %+v", released)
	}

	job, ok, err = store.Claim(ctx, workerID)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if n, err := store.Recover(ctx, 2*time.Minute); err != nil || n != 0 {
		t.Fatalf("fresh recover n=%d err=%v", n, err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE import_jobs SET locked_at = now() - interval '3 minutes' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	n, err := store.Recover(ctx, 2*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("expired recover n=%d err=%v", n, err)
	}
	recovered, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != StatusQueued || recovered.Attempt != 1 || recovered.LastError == nil || *recovered.LastError != "lease expired" {
		t.Fatalf("recovered = %+v", recovered)
	}

	job, ok, err = store.Claim(ctx, workerID)
	if err != nil || !ok || job.ID != enqueued.Job.ID {
		t.Fatal(err, ok, job.ID)
	}
	if _, err := testPool.Exec(ctx, `UPDATE import_jobs SET max_attempts = attempt, locked_at = now() - interval '3 minutes' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Recover(ctx, 2*time.Minute); err != nil {
		t.Fatal(err)
	}
	failed, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed {
		t.Fatalf("exhausted recover = %s", failed.Status)
	}
	if _, err := store.Retry(ctx, failed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Retry(ctx, failed.ID); err != ErrNotRetryable {
		t.Fatalf("second retry = %v", err)
	}
}

func TestRequeueBackoff(t *testing.T) {
	ctx := context.Background()
	pushOtherJobs(t)
	store := NewStore(testPool)
	enqueued, err := store.Enqueue(ctx, "queue-test", newID(t))
	if err != nil {
		t.Fatal(err)
	}
	workerID := newID(t)
	job, ok, err := store.Claim(ctx, workerID)
	if err != nil || !ok || job.ID != enqueued.Job.ID {
		t.Fatalf("claim ok=%v err=%v id=%s", ok, err, job.ID)
	}
	status, err := store.Requeue(ctx, job.ID, workerID, job.Attempt, "Temporary failure")
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusQueued {
		t.Fatalf("status %s", status)
	}
	again, err := store.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !again.NextAttemptAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("next attempt %s", again.NextAttemptAt)
	}
	var due int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM import_jobs WHERE id = $1 AND next_attempt_at <= now()`, enqueued.Job.ID).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != 0 {
		t.Fatal("backoff job is already due")
	}
}

func newID(t *testing.T) string {
	t.Helper()
	id, err := platform.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func duplicateDatabase(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P04"
}

func migrationsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "migrations"
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "migrations")
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
