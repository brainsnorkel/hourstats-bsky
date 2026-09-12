package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAttachSourceSQL_EscapesSingleQuotes(t *testing.T) {
	got, err := attachSourceSQL("/data/o'brien/hourstats-prod.db")
	if err != nil {
		t.Fatalf("attachSourceSQL: %v", err)
	}
	want := `ATTACH DATABASE 'file:/data/o''brien/hourstats-prod.db?mode=ro' AS src`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAttachSourceSQL_RejectsQuestionMarkAndNUL(t *testing.T) {
	// A '?' would be read as the start of the URI query string, so the path
	// could smuggle its own pragmas (mode=rwc, vfs=...) into the attach.
	for _, path := range []string{
		"/data/hourstats.db?mode=rwc",
		"/data/hourstats.db\x00truncated",
	} {
		if _, err := attachSourceSQL(path); err == nil {
			t.Errorf("attachSourceSQL(%q) = nil error, want rejection", path)
		}
	}
}

func TestAttachSourceSQL_PlainPathUnchanged(t *testing.T) {
	got, err := attachSourceSQL("/data/hourstats-prod.db")
	if err != nil {
		t.Fatalf("attachSourceSQL: %v", err)
	}
	if !strings.Contains(got, `'file:/data/hourstats-prod.db?mode=ro'`) {
		t.Errorf("unexpected statement: %s", got)
	}
}

// TestBackup_QuotedDataDir runs a real backup out of a directory whose name
// contains a single quote — the case that broke the ATTACH statement before it
// was escaped.
func TestBackup_QuotedDataDir(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "o'brien data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	dbPath := filepath.Join(dataDir, "hourstats-test.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New(%q): %v", dbPath, err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.SetKeyValue(ctx, "backup_probe", "present"); err != nil {
		t.Fatalf("SetKeyValue: %v", err)
	}

	backupPath, err := s.Backup(ctx, dataDir, "test", 7)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dest, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer dest.Close()

	var value string
	if err := dest.QueryRowContext(ctx, `SELECT value FROM key_value WHERE key = 'backup_probe'`).Scan(&value); err != nil {
		t.Fatalf("read backup key_value: %v", err)
	}
	if value != "present" {
		t.Errorf("value = %q, want %q", value, "present")
	}
}
