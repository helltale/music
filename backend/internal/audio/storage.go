package audio

import (
	"context"
	"io"
	"time"
)

// ObjectStorage is the S3-compatible store for original and playable audio.
// Callers do not depend on a MinIO SDK.
type ObjectStorage interface {
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (url string, expiresAt time.Time, err error)
}
