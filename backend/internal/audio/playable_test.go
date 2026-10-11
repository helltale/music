package audio

import (
	"context"
	"testing"
)

func TestPlayableRecordings(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testPool)
	readyID := newID(t)
	failedID := newID(t)
	if _, err := store.Ensure(ctx, readyID, ProviderLocal, "ready.wav"); err != nil {
		t.Fatal(err)
	}
	generation, err := store.BeginAttempt(ctx, readyID)
	if err != nil {
		t.Fatal(err)
	}
	key := "recordings/" + readyID + "/g1/playable.m4a"
	if err := store.PublishReady(ctx, Asset{
		RecordingID:       readyID,
		Generation:        generation,
		PlayableObjectKey: &key,
		Status:            StatusReady,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(ctx, failedID, ProviderLocal, "failed.wav"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkFailed(ctx, failedID, 0, "Audio source not found"); err != nil {
		t.Fatal(err)
	}
	ready, err := store.PlayableRecordings(ctx, []string{readyID, failedID, newID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !ready[readyID] || ready[failedID] || len(ready) != 1 {
		t.Fatalf("ready = %+v", ready)
	}
	empty, err := store.PlayableRecordings(ctx, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty = %+v err %v", empty, err)
	}
}
