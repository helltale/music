package audio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Processor turns one recording's original audio into a playable object.
// Import jobs call it later. This phase does not update import_jobs.
type Processor struct {
	DB       *pgxpool.Pool
	Objects  ObjectStorage
	Source   Provider
	TempDir  string
	MaxBytes int64
	Timeout  time.Duration
	// AfterPublish runs inside the READY transaction, after the asset row matches
	// this generation. Phase 7 uses it for the job progress update. Nil skips it.
	AfterPublish func(ctx context.Context, tx pgx.Tx) error
}

// Process reads the provider file, stores original and playable audio, and marks the asset READY.
// workID is the temp directory segment ({TempDir}/{workID}/{recordingID}/).
// The same checksum on an existing READY asset does not upload again.
func (p *Processor) Process(ctx context.Context, recordingID, sourceProvider, externalID, workID string) (Asset, error) {
	store := NewStore(p.DB)
	work, err := p.stage(ctx, recordingID, externalID, workID)
	if err != nil {
		return p.failEarly(ctx, store, recordingID, sourceProvider, err)
	}
	defer os.RemoveAll(work.dir)

	asset, err := store.Ensure(ctx, recordingID, sourceProvider, work.reference)
	if err != nil {
		return Asset{}, err
	}
	if asset.Status == StatusReady && asset.Checksum != nil && *asset.Checksum == work.checksum {
		return asset, nil
	}

	generation, err := store.BeginAttempt(ctx, recordingID)
	if err != nil {
		return Asset{}, err
	}
	procCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	ready, err := p.encodeAndStore(procCtx, store, recordingID, generation, work)
	if err != nil {
		if errors.Is(err, ErrSuperseded) {
			return Asset{}, err
		}
		message, permanent := classify(ctx, procCtx, err)
		if markErr := store.MarkFailed(context.WithoutCancel(ctx), recordingID, generation, message); markErr != nil && !errors.Is(markErr, ErrSuperseded) {
			return Asset{}, markErr
		}
		return Asset{}, &ProcessError{Message: message, Err: err, Permanent: permanent}
	}
	return ready, nil
}

type staged struct {
	dir         string
	original    string
	reference   string
	checksum    string
	size        int64
	contentType string
	ext         string
}

func (p *Processor) stage(ctx context.Context, recordingID, externalID, workID string) (staged, error) {
	source, reference, err := p.Source.Open(ctx, externalID)
	if err != nil {
		return staged{}, err
	}
	defer source.Close()

	dir := filepath.Join(p.TempDir, workID, recordingID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return staged{}, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			os.RemoveAll(dir)
		}
	}()
	ext := filepath.Ext(reference)
	if ext == "" {
		ext = ".bin"
	}
	original := filepath.Join(dir, "original"+ext)
	file, err := os.Create(original)
	if err != nil {
		return staged{}, err
	}
	hash := sha256.New()
	limit := p.MaxBytes
	if limit <= 0 {
		limit = 1
	}
	written, copyErr := io.Copy(file, io.TeeReader(io.LimitReader(source, limit+1), hash))
	closeErr := file.Close()
	if copyErr != nil {
		return staged{}, copyErr
	}
	if closeErr != nil {
		return staged{}, closeErr
	}
	if written == 0 {
		return staged{}, &ProcessError{Message: "Audio file is empty", Permanent: true}
	}
	if written > limit {
		return staged{}, &ProcessError{Message: "Audio file is too large", Permanent: true}
	}
	if _, err := probeFile(ctx, original); err != nil {
		return staged{}, &ProcessError{Message: "Audio stream not found", Err: err, Permanent: true}
	}
	cleanup = false
	return staged{
		dir:         dir,
		original:    original,
		reference:   reference,
		checksum:    hex.EncodeToString(hash.Sum(nil)),
		size:        written,
		contentType: contentType(ext),
		ext:         ext,
	}, nil
}

func (p *Processor) encodeAndStore(ctx context.Context, store *Store, recordingID string, generation int, work staged) (Asset, error) {
	if err := store.MarkProcessing(ctx, recordingID, generation); err != nil {
		return Asset{}, err
	}
	playablePath := filepath.Join(work.dir, "playable.m4a")
	if err := transcode(ctx, work.original, playablePath); err != nil {
		return Asset{}, err
	}
	info, err := probeFile(ctx, playablePath)
	if err != nil {
		return Asset{}, err
	}
	stat, err := os.Stat(playablePath)
	if err != nil {
		return Asset{}, err
	}
	originalKey, playableKey := objectKeys(recordingID, generation, work.ext)
	if err := putFile(ctx, p.Objects, originalKey, work.contentType, work.original, work.size); err != nil {
		return Asset{}, err
	}
	if err := putFile(ctx, p.Objects, playableKey, "audio/mp4", playablePath, stat.Size()); err != nil {
		return Asset{}, err
	}
	codec := "aac"
	container := "mp4"
	bitrate := info.Bitrate
	if bitrate == 0 {
		bitrate = 192000
	}
	size := stat.Size()
	asset := Asset{
		RecordingID:       recordingID,
		Generation:        generation,
		OriginalObjectKey: &originalKey,
		PlayableObjectKey: &playableKey,
		Codec:             &codec,
		Container:         &container,
		Bitrate:           &bitrate,
		SampleRate:        &info.SampleRate,
		Channels:          &info.Channels,
		DurationMS:        &info.DurationMS,
		SizeBytes:         &size,
		Checksum:          &work.checksum,
		Status:            StatusReady,
	}
	if err := p.publish(ctx, asset); err != nil {
		return Asset{}, err
	}
	return store.GetByRecording(ctx, recordingID)
}

func (p *Processor) publish(ctx context.Context, asset Asset) error {
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := NewStore(tx).PublishReady(ctx, asset); err != nil {
		return err
	}
	if p.AfterPublish != nil {
		if err := p.AfterPublish(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Processor) failEarly(ctx context.Context, store *Store, recordingID, sourceProvider string, cause error) (Asset, error) {
	message, permanent := classify(ctx, nil, cause)
	if _, err := store.Ensure(ctx, recordingID, sourceProvider, ""); err != nil {
		return Asset{}, err
	}
	current, err := store.GetByRecording(ctx, recordingID)
	if err != nil {
		return Asset{}, err
	}
	if err := store.MarkFailed(ctx, recordingID, current.Generation, message); err != nil && !errors.Is(err, ErrSuperseded) {
		return Asset{}, err
	}
	return Asset{}, &ProcessError{Message: message, Err: cause, Permanent: permanent}
}

func putFile(ctx context.Context, objects ObjectStorage, key, contentType, path string, size int64) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return objects.Put(ctx, key, contentType, file, size)
}

func objectKeys(recordingID string, generation int, ext string) (string, string) {
	prefix := fmt.Sprintf("recordings/%s/g%d/", recordingID, generation)
	return prefix + "original" + ext, prefix + "playable.m4a"
}

func contentType(ext string) string {
	if strings.EqualFold(ext, ".wav") {
		return "audio/wav"
	}
	return "application/octet-stream"
}

func (p *Processor) timeout() time.Duration {
	if p.Timeout <= 0 {
		return 10 * time.Minute
	}
	return p.Timeout
}

func classify(parent, limited context.Context, err error) (string, bool) {
	var process *ProcessError
	if errors.As(err, &process) && process.Message != "" {
		return process.Message, process.Permanent
	}
	if errors.Is(err, ErrSourceNotFound) {
		return "Audio source not found", true
	}
	if parent.Err() == nil && limited != nil && limited.Err() != nil {
		return "Audio processing timed out", false
	}
	var encoded *ffmpegError
	if errors.As(err, &encoded) {
		return "Audio processing failed", true
	}
	return "Audio processing failed", false
}
