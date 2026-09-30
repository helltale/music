package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeDB struct {
	err   error
	calls int
}

func (f *fakeDB) Ping(context.Context) error {
	f.calls++
	return f.err
}

func TestHealthDoesNotPing(t *testing.T) {
	db := &fakeDB{}
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	Health(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if db.calls != 0 {
		t.Fatal("health pinged the database")
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("body %v", body)
	}
}

func TestReadyStates(t *testing.T) {
	db := &fakeDB{}
	ready := &Ready{DB: db}
	rec := httptest.NewRecorder()
	ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status %d", rec.Code)
	}

	db.err = errors.New("down")
	rec = httptest.NewRecorder()
	ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("down status %d", rec.Code)
	}

	calls := db.calls
	ready.ShuttingDown.Store(true)
	rec = httptest.NewRecorder()
	ready.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("shutdown status %d", rec.Code)
	}
	if db.calls != calls {
		t.Fatal("shutdown readiness pinged the database")
	}
}

func TestMiddlewareRequestIDAndNotFound(t *testing.T) {
	log := NewLogger()
	handler := Middleware(log, http.HandlerFunc(NotFound))
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	req.Header.Set("X-Request-Id", "abc-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("X-Request-Id") != "abc-123" {
		t.Fatalf("request id %q", rec.Header().Get("X-Request-Id"))
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Fatalf("code %s", body.Error.Code)
	}
}

func TestMiddlewareReplacesBadRequestID(t *testing.T) {
	handler := Middleware(NewLogger(), http.HandlerFunc(Health))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-Id", "bad id\r\n")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	got := rec.Header().Get("X-Request-Id")
	if !uuidPattern.MatchString(got) {
		t.Fatalf("request id %q", got)
	}
}

func TestProbeGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		Health(w, r)
	}))
	t.Cleanup(srv.Close)
	if ProbeGET(srv.Listener.Addr().String(), "/health") != 0 {
		t.Fatal("expected success")
	}
	if ProbeGET(srv.Listener.Addr().String(), "/missing") != 1 {
		t.Fatal("expected failure")
	}
}

func TestListenErr(t *testing.T) {
	if ListenErr(nil) != nil {
		t.Fatal("nil")
	}
	if ListenErr(http.ErrServerClosed) != nil {
		t.Fatal("closed")
	}
	if ListenErr(errors.New("boom")) == nil {
		t.Fatal("boom")
	}
}

func TestNewServerTimeouts(t *testing.T) {
	srv := NewServer(":8080", http.NewServeMux())
	if srv.ReadTimeout != 15*time.Second || srv.WriteTimeout != 15*time.Second {
		t.Fatalf("timeouts read=%s write=%s", srv.ReadTimeout, srv.WriteTimeout)
	}
}
