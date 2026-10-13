package importjob

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/helltale/music/backend/internal/catalog"
)

type catalogDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (w *Worker) artists() *catalog.Store {
	return catalog.NewStore(w.DB.(catalogDB))
}

// schedule enqueues one due automatic sync through the existing artist import job.
func (w *Worker) schedule(ctx context.Context) error {
	artistID, ok, err := w.artists().ClaimDueSync(ctx, w.SyncInterval)
	if err != nil || !ok {
		return err
	}
	providerName, externalID, err := w.artists().SyncSubject(ctx, artistID)
	if err != nil {
		w.log().Warn("skip automatic sync", "artist_id", artistID, "error", err.Error())
		return nil
	}
	if _, known := w.Catalogs[providerName]; !known {
		w.log().Warn("skip automatic sync", "artist_id", artistID, "provider", providerName)
		return nil
	}
	result, err := w.Store.EnqueueSync(ctx, providerName, externalID)
	if err != nil {
		return err
	}
	w.log().Info("scheduled sync", "artist_id", artistID, "job_id", result.Job.ID, "created", result.Created)
	return nil
}

func (w *Worker) noteSync(ctx context.Context, job Job) {
	var doc progressDocument
	if len(job.Progress) == 0 || json.Unmarshal(job.Progress, &doc) != nil || doc.ArtistID == "" {
		return
	}
	interval := w.SyncInterval
	if interval <= 0 {
		interval = 0
	}
	if err := w.artists().RecordSync(ctx, doc.ArtistID, interval); err != nil && !errors.Is(err, catalog.ErrNotFound) {
		w.log().Error("record sync", "job_id", job.ID, "artist_id", doc.ArtistID, "error", err.Error())
	}
}
