package audio

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Config points an S3 client at MinIO or another S3-compatible store.
// Endpoint is used for Put and Delete. PublicEndpoint is the host written into
// presigned GET URLs. An empty public endpoint uses Endpoint.
type S3Config struct {
	Endpoint       string
	PublicEndpoint string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	UsePathStyle   bool
}

// S3Store implements ObjectStorage with the AWS S3 API.
type S3Store struct {
	bucket  string
	client  *s3.Client
	presign *s3.PresignClient
}

// NewS3Store builds a path-style client. Put uses Endpoint. Presign uses PublicEndpoint.
func NewS3Store(cfg S3Config) (*S3Store, error) {
	client, err := newClient(cfg, cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	presignEndpoint := cfg.Endpoint
	if cfg.PublicEndpoint != "" {
		presignEndpoint = cfg.PublicEndpoint
	}
	presignClient := client
	if presignEndpoint != cfg.Endpoint {
		presignClient, err = newClient(cfg, presignEndpoint)
		if err != nil {
			return nil, err
		}
	}
	return &S3Store{
		bucket:  cfg.Bucket,
		client:  client,
		presign: s3.NewPresignClient(presignClient),
	}, nil
}

func newClient(cfg S3Config, endpoint string) (*s3.Client, error) {
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}
	return s3.New(s3.Options{
		Region:       region,
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: cfg.UsePathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}), nil
}

// Put writes one object. size is the exact body length.
func (s *S3Store) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// Delete removes one object. A missing object is an error from the store.
func (s *S3Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("delete object: %w", err)
	}
	return nil
}

// PresignGet returns a URL for a GET that expires after ttl.
func (s *S3Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	out, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("presign object: %w", err)
	}
	return out.URL, time.Now().Add(ttl), nil
}
