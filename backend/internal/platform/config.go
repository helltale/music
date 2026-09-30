package platform

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr                  = ":8080"
	defaultWorkerHealthAddr          = ":8081"
	defaultS3Endpoint                = "http://minio:9000"
	defaultS3PublicEndpoint          = "http://localhost:9000"
	defaultS3Bucket                  = "music"
	defaultS3Region                  = "us-east-1"
	defaultAudioTempDir              = "/tmp/music-audio"
	defaultAudioMaxBytes             = 209715200
	defaultAudioProcessTimeout       = 10 * time.Minute
	defaultPlaybackURLTTL            = time.Hour
	defaultLocalAudioDir             = "/app/testdata/audio"
	defaultLeaseTimeout              = 2 * time.Minute
	defaultLeaseHeartbeat            = 30 * time.Second
	defaultMigrationsDir             = "/app/migrations"
	defaultShutdownTimeout           = 25 * time.Second
	maxRequestBytes            int64 = 1 << 20
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Config is process configuration loaded from the environment.
// Secret values are not printed by String.
type Config struct {
	DatabaseURL         string
	HTTPAddr            string
	WorkerHealthAddr    string
	WorkerInstanceID    string
	S3Endpoint          string
	S3PublicEndpoint    string
	S3Bucket            string
	S3Region            string
	S3AccessKey         string
	S3SecretKey         string
	S3UseSSL            bool
	S3UsePathStyle      bool
	AudioTempDir        string
	AudioMaxBytes       int64
	AudioProcessTimeout time.Duration
	PlaybackURLTTL      time.Duration
	LocalAudioDir       string
	LeaseTimeout        time.Duration
	LeaseHeartbeat      time.Duration
	MigrationsDir       string
	ShutdownTimeout     time.Duration
}

// Load reads configuration from the environment.
// DATABASE_URL is required. Other variables fall back to local-compose defaults.
func Load() (Config, error) {
	var cfg Config
	var err error

	cfg.DatabaseURL = strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	cfg.HTTPAddr = envString("HTTP_ADDR", defaultHTTPAddr)
	cfg.WorkerHealthAddr = envString("WORKER_HEALTH_ADDR", defaultWorkerHealthAddr)
	cfg.S3Endpoint = envString("S3_ENDPOINT", defaultS3Endpoint)
	cfg.S3PublicEndpoint = envString("S3_PUBLIC_ENDPOINT", defaultS3PublicEndpoint)
	cfg.S3Bucket = envString("S3_BUCKET", defaultS3Bucket)
	cfg.S3Region = envString("S3_REGION", defaultS3Region)
	cfg.S3AccessKey = os.Getenv("S3_ACCESS_KEY")
	cfg.S3SecretKey = os.Getenv("S3_SECRET_KEY")
	cfg.AudioTempDir = envString("AUDIO_TEMP_DIR", defaultAudioTempDir)
	cfg.LocalAudioDir = envString("LOCAL_AUDIO_DIR", defaultLocalAudioDir)
	cfg.MigrationsDir = envString("MIGRATIONS_DIR", defaultMigrationsDir)

	if cfg.S3UseSSL, err = envBool("S3_USE_SSL", false); err != nil {
		return Config{}, err
	}
	if cfg.S3UsePathStyle, err = envBool("S3_USE_PATH_STYLE", true); err != nil {
		return Config{}, err
	}
	if cfg.AudioMaxBytes, err = envInt64("AUDIO_MAX_BYTES", defaultAudioMaxBytes); err != nil {
		return Config{}, err
	}
	if cfg.AudioProcessTimeout, err = envDuration("AUDIO_PROCESS_TIMEOUT", defaultAudioProcessTimeout); err != nil {
		return Config{}, err
	}
	if cfg.PlaybackURLTTL, err = envDuration("PLAYBACK_URL_TTL", defaultPlaybackURLTTL); err != nil {
		return Config{}, err
	}
	if cfg.LeaseTimeout, err = envDuration("LEASE_TIMEOUT", defaultLeaseTimeout); err != nil {
		return Config{}, err
	}
	if cfg.LeaseHeartbeat, err = envDuration("LEASE_HEARTBEAT", defaultLeaseHeartbeat); err != nil {
		return Config{}, err
	}
	if cfg.ShutdownTimeout, err = envDuration("SHUTDOWN_TIMEOUT", defaultShutdownTimeout); err != nil {
		return Config{}, err
	}
	if cfg.LeaseHeartbeat >= cfg.LeaseTimeout {
		return Config{}, fmt.Errorf("LEASE_HEARTBEAT must be shorter than LEASE_TIMEOUT")
	}

	id := strings.TrimSpace(os.Getenv("WORKER_INSTANCE_ID"))
	if id == "" {
		id, err = NewUUID()
		if err != nil {
			return Config{}, fmt.Errorf("worker instance id: %w", err)
		}
	} else if !uuidPattern.MatchString(id) {
		return Config{}, fmt.Errorf("WORKER_INSTANCE_ID must be a UUID")
	}
	cfg.WorkerInstanceID = strings.ToLower(id)
	return cfg, nil
}

func envString(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envBool(key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s: invalid boolean %q", key, v)
	}
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}

func envInt64(key string, fallback int64) (int64, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if n <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return n, nil
}
