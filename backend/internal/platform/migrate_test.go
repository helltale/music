package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationFilesSortsAndSkipsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"0002_b.up.sql", "0001_a.up.sql", "notes.txt", "0003_down.down.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- ok\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := MigrationFiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v", files)
	}
	if filepath.Base(files[0]) != "0001_a.up.sql" || filepath.Base(files[1]) != "0002_b.up.sql" {
		t.Fatalf("order = %v", files)
	}
}

func TestMigrationFilesMissingDir(t *testing.T) {
	if _, err := MigrationFiles(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error")
	}
}
