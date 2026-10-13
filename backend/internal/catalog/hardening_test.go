package catalog

import (
	"context"
	"testing"
)

func TestSearchDoesNotTreatWildcardsAsPatterns(t *testing.T) {
	ctx := context.Background()
	store := NewStore(testPool)
	literalID := newID(t)
	plainID := newID(t)
	literalName := "lit" + literalID + "%"
	plainName := "plain" + plainID
	if _, err := store.CreateArtist(ctx, Artist{ID: literalID, Name: literalName}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateArtist(ctx, Artist{ID: plainID, Name: plainName}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM artists WHERE id = ANY($1)`, []string{literalID, plainID})
	})

	percent, _, err := store.ListArtists(ctx, literalID+"%", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !namesContain(percent, literalName) || namesContain(percent, plainName) {
		t.Fatalf("percent search = %v", names(percent))
	}
	underscore, _, err := store.ListArtists(ctx, "_", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if namesContain(underscore, literalName) || namesContain(underscore, plainName) {
		t.Fatalf("underscore search = %v", names(underscore))
	}
}

func names(items []Artist) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Name
	}
	return out
}

func namesContain(items []Artist, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return true
		}
	}
	return false
}
