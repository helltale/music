package importjob

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/helltale/music/backend/internal/catalog"
	"github.com/helltale/music/backend/internal/catalog/provider"
)

// CatalogSource is the provider data a worker reads before it writes the catalog.
type CatalogSource interface {
	GetArtist(ctx context.Context, externalID string) (provider.Artist, error)
	GetArtistReleases(ctx context.Context, externalID string) ([]provider.ReleaseSummary, error)
	GetReleaseDetails(ctx context.Context, externalID string) (provider.Release, error)
}

// Worker claims jobs and imports their catalog.
// Audio is a later phase: a finished catalog with no audio requirement is COMPLETED.
type Worker struct {
	Store *Store
	DB    interface {
		Begin(context.Context) (pgx.Tx, error)
	}
	Catalogs  map[string]CatalogSource
	WorkerID  string
	Lease     time.Duration
	Heartbeat time.Duration
	Poll      time.Duration
	Log       *slog.Logger
}

// Run claims and finishes jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	poll := w.Poll
	if poll <= 0 {
		poll = time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if _, err := w.Store.Recover(ctx, w.Lease); err != nil && ctx.Err() == nil {
			w.log().Error("recover jobs", "error", err.Error())
		}
		job, ok, err := w.Store.Claim(ctx, w.WorkerID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.log().Error("claim job", "error", err.Error())
			if !sleep(ctx, poll) {
				return nil
			}
			continue
		}
		if !ok {
			if !sleep(ctx, poll) {
				return nil
			}
			continue
		}
		w.process(ctx, job)
	}
}

func (w *Worker) process(ctx context.Context, job Job) {
	jobCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	leaseLost := make(chan struct{}, 1)
	stopBeat := make(chan struct{})
	defer close(stopBeat)
	go w.beat(jobCtx, job, leaseLost, stopBeat, cancel)

	progress, err := w.importCatalog(jobCtx, job)
	if len(progress) > 0 {
		job.Progress = progress
	}
	lost := len(leaseLost) > 0 || errors.Is(err, ErrLeaseLost)
	if lost {
		w.log().Warn("lease lost", "job_id", job.ID)
		return
	}
	if err == nil {
		if finishErr := w.Store.Finish(ctx, job.ID, w.WorkerID, job.Attempt, StatusCompleted, "", job.Progress); finishErr != nil {
			if !errors.Is(finishErr, ErrLeaseLost) {
				w.log().Error("complete job", "job_id", job.ID, "error", finishErr.Error())
			}
		}
		return
	}
	if ctx.Err() != nil {
		if releaseErr := w.Store.Release(context.WithoutCancel(ctx), job.ID, w.WorkerID, job.Attempt); releaseErr != nil {
			w.log().Error("release job", "job_id", job.ID, "error", releaseErr.Error())
		}
		return
	}
	message := jobMessage(err)
	w.log().Error("import job", "job_id", job.ID, "error", err.Error(), "permanent", permanent(err))
	if permanent(err) {
		_ = w.Store.Finish(ctx, job.ID, w.WorkerID, job.Attempt, StatusFailed, message, job.Progress)
		return
	}
	if _, requeueErr := w.Store.Requeue(ctx, job.ID, w.WorkerID, job.Attempt, message); requeueErr != nil && !errors.Is(requeueErr, ErrLeaseLost) {
		w.log().Error("requeue job", "job_id", job.ID, "error", requeueErr.Error())
	}
}

func (w *Worker) beat(ctx context.Context, job Job, leaseLost chan struct{}, stop <-chan struct{}, cancel context.CancelFunc) {
	if w.Heartbeat <= 0 {
		return
	}
	ticker := time.NewTicker(w.Heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ok, err := w.Store.Heartbeat(ctx, job.ID, w.WorkerID, job.Attempt)
			if err != nil {
				w.log().Error("heartbeat", "job_id", job.ID, "error", err.Error())
				continue
			}
			if !ok {
				leaseLost <- struct{}{}
				cancel()
				return
			}
		}
	}
}

func (w *Worker) importCatalog(ctx context.Context, job Job) (json.RawMessage, error) {
	source, ok := w.Catalogs[job.Provider]
	if !ok {
		return nil, errUnknownProvider
	}
	body, err := snapshot(ctx, source, job.Provider, job.ExternalID)
	if err != nil {
		return nil, err
	}
	var latest json.RawMessage
	importer := catalog.NewImporter(w.DB)
	importer.Notify(func(ctx context.Context, progress catalog.ImportProgress) error {
		raw, err := json.Marshal(progressDocument{
			ArtistID:           progress.ArtistID,
			ReleasesTotal:      progress.ReleasesTotal,
			ReleasesDone:       progress.ReleasesDone,
			RecordingsTotal:    progress.RecordingsTotal,
			RecordingsImported: progress.RecordingsImported,
		})
		if err != nil {
			return err
		}
		latest = raw
		return w.Store.SaveProgress(ctx, job.ID, w.WorkerID, job.Attempt, raw)
	})
	result, err := importer.ImportArtist(ctx, body)
	if err != nil {
		return latest, err
	}
	for _, row := range result.Ambiguous {
		w.log().Warn("ambiguous isrc", "job_id", job.ID, "external_id", row.ExternalID, "isrc", row.ISRC, "recording_ids", row.RecordingIDs)
	}
	return latest, nil
}

type progressDocument struct {
	ArtistID           string `json:"artist_id"`
	ReleasesTotal      int    `json:"releases_total"`
	ReleasesDone       int    `json:"releases_done"`
	RecordingsTotal    int    `json:"recordings_total"`
	RecordingsImported int    `json:"recordings_imported"`
}

func snapshot(ctx context.Context, source CatalogSource, providerName, externalID string) (catalog.ArtistImport, error) {
	artist, err := source.GetArtist(ctx, externalID)
	if err != nil {
		return catalog.ArtistImport{}, err
	}
	summaries, err := source.GetArtistReleases(ctx, externalID)
	if err != nil {
		return catalog.ArtistImport{}, err
	}
	releases := make([]catalog.ReleaseImport, 0, len(summaries))
	for _, summary := range summaries {
		details, err := source.GetReleaseDetails(ctx, summary.ExternalID)
		if err != nil {
			return catalog.ArtistImport{}, err
		}
		tracks := make([]catalog.TrackImport, 0, len(details.Tracks))
		for _, track := range details.Tracks {
			tracks = append(tracks, catalog.TrackImport{
				ExternalID:    track.ExternalID,
				Title:         track.Title,
				ISRC:          track.ISRC,
				DurationMS:    track.DurationMS,
				DiscNumber:    track.DiscNumber,
				TrackNumber:   track.TrackNumber,
				TitleOverride: track.TitleOverride,
				Artists:       toCredits(track.Artists),
			})
		}
		releases = append(releases, catalog.ReleaseImport{
			ExternalID:  details.ExternalID,
			Title:       details.Title,
			ReleaseType: details.ReleaseType,
			ReleaseDate: details.ReleaseDate,
			Artists:     toCredits(details.Artists),
			Tracks:      tracks,
		})
	}
	return catalog.ArtistImport{
		Provider:    providerName,
		ExternalID:  artist.ExternalID,
		Name:        artist.Name,
		Description: artist.Description,
		Releases:    releases,
	}, nil
}

func toCredits(credits []provider.Credit) []catalog.Credit {
	out := make([]catalog.Credit, len(credits))
	for i, credit := range credits {
		out[i] = catalog.Credit{ExternalID: credit.ExternalID, Name: credit.Name, Role: credit.Role}
	}
	return out
}

var errUnknownProvider = errors.New("unknown provider")

func permanent(err error) bool {
	if errors.Is(err, provider.ErrNotFound) || errors.Is(err, errUnknownProvider) {
		return true
	}
	var invalid *catalog.InvalidInputError
	return errors.As(err, &invalid)
}

func jobMessage(err error) string {
	switch {
	case errors.Is(err, provider.ErrNotFound):
		return "Artist not found"
	case errors.Is(err, errUnknownProvider):
		return "Unknown provider"
	default:
		var invalid *catalog.InvalidInputError
		if errors.As(err, &invalid) {
			return "Provider response is invalid"
		}
		return "Temporary failure"
	}
}

func (w *Worker) log() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
