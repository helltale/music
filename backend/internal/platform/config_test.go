package platform

import "testing"

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://music:music@postgres:5432/music")
	t.Setenv("WORKER_INSTANCE_ID", "")
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("S3_USE_SSL", "")
	t.Setenv("S3_USE_PATH_STYLE", "")
	t.Setenv("PLAYBACK_URL_TTL", "")
	t.Setenv("LEASE_TIMEOUT", "")
	t.Setenv("LEASE_HEARTBEAT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.S3UseSSL {
		t.Fatal("S3UseSSL default")
	}
	if !cfg.S3UsePathStyle {
		t.Fatal("S3UsePathStyle default")
	}
	if cfg.PlaybackURLTTL.String() != "1h0m0s" {
		t.Fatalf("ttl = %s", cfg.PlaybackURLTTL)
	}
	if cfg.LeaseHeartbeat >= cfg.LeaseTimeout {
		t.Fatal("heartbeat must be shorter than lease")
	}
	if !uuidPattern.MatchString(cfg.WorkerInstanceID) {
		t.Fatalf("instance id %q", cfg.WorkerInstanceID)
	}
}

func TestLoadRejectsBadBoolAndInstanceID(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://music:music@postgres:5432/music")
	t.Setenv("S3_USE_SSL", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("expected bool error")
	}

	t.Setenv("S3_USE_SSL", "true")
	t.Setenv("WORKER_INSTANCE_ID", "not-a-uuid")
	if _, err := Load(); err == nil {
		t.Fatal("expected uuid error")
	}
}

func TestLoadRejectsHeartbeatLongerThanLease(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://music:music@postgres:5432/music")
	t.Setenv("LEASE_TIMEOUT", "10s")
	t.Setenv("LEASE_HEARTBEAT", "10s")
	if _, err := Load(); err == nil {
		t.Fatal("expected lease error")
	}
}
