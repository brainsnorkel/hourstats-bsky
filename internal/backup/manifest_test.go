package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadManifest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	original := Manifest{
		BackupTimestamp: "2026-02-07T10-00-00Z",
		BackupVersion:   "1.0",
		TotalItems:      150,
		Checksum:        "abc123",
		Tables: []TableManifest{
			{
				TableName:      "hourstats-state",
				ItemCount:      100,
				FileSize:       2048,
				FileName:       "hourstats-state.jsonl",
				Checksum:       "def456",
				BackupDuration: "5s",
			},
			{
				TableName:      "sentiment_history",
				ItemCount:      50,
				FileSize:       1024,
				FileName:       "sentiment_history.jsonl",
				Checksum:       "ghi789",
				BackupDuration: "3s",
			},
		},
	}

	if err := WriteManifest(path, original); err != nil {
		t.Fatalf("WriteManifest failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("manifest file was not created")
	}

	// Read back
	got, err := ReadManifest(path)
	if err != nil {
		t.Fatalf("ReadManifest failed: %v", err)
	}

	// Verify fields
	if got.BackupTimestamp != original.BackupTimestamp {
		t.Errorf("BackupTimestamp = %q, want %q", got.BackupTimestamp, original.BackupTimestamp)
	}
	if got.BackupVersion != original.BackupVersion {
		t.Errorf("BackupVersion = %q, want %q", got.BackupVersion, original.BackupVersion)
	}
	if got.TotalItems != original.TotalItems {
		t.Errorf("TotalItems = %d, want %d", got.TotalItems, original.TotalItems)
	}
	if got.Checksum != original.Checksum {
		t.Errorf("Checksum = %q, want %q", got.Checksum, original.Checksum)
	}
	if len(got.Tables) != len(original.Tables) {
		t.Fatalf("Tables count = %d, want %d", len(got.Tables), len(original.Tables))
	}
	for i, table := range got.Tables {
		orig := original.Tables[i]
		if table.TableName != orig.TableName {
			t.Errorf("Tables[%d].TableName = %q, want %q", i, table.TableName, orig.TableName)
		}
		if table.ItemCount != orig.ItemCount {
			t.Errorf("Tables[%d].ItemCount = %d, want %d", i, table.ItemCount, orig.ItemCount)
		}
		if table.FileSize != orig.FileSize {
			t.Errorf("Tables[%d].FileSize = %d, want %d", i, table.FileSize, orig.FileSize)
		}
		if table.FileName != orig.FileName {
			t.Errorf("Tables[%d].FileName = %q, want %q", i, table.FileName, orig.FileName)
		}
		if table.Checksum != orig.Checksum {
			t.Errorf("Tables[%d].Checksum = %q, want %q", i, table.Checksum, orig.Checksum)
		}
	}
}

func TestReadManifest_NotFound(t *testing.T) {
	_, err := ReadManifest("/nonexistent/path/manifest.json")
	if err == nil {
		t.Error("expected error for nonexistent manifest, got nil")
	}
}

func TestReadManifest_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	_, err := ReadManifest(path)
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestCalculateFileChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")

	content := []byte("hello world\n")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	checksum, err := CalculateFileChecksum(path)
	if err != nil {
		t.Fatalf("CalculateFileChecksum failed: %v", err)
	}

	if checksum == "" {
		t.Error("checksum is empty")
	}

	// SHA256 hex string should be 64 characters
	if len(checksum) != 64 {
		t.Errorf("checksum length = %d, want 64", len(checksum))
	}

	// Same content should produce same checksum
	path2 := filepath.Join(dir, "test2.txt")
	if err := os.WriteFile(path2, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	checksum2, err := CalculateFileChecksum(path2)
	if err != nil {
		t.Fatalf("CalculateFileChecksum failed: %v", err)
	}

	if checksum != checksum2 {
		t.Errorf("same content produced different checksums: %q vs %q", checksum, checksum2)
	}

	// Different content should produce different checksum
	path3 := filepath.Join(dir, "test3.txt")
	if err := os.WriteFile(path3, []byte("different content"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	checksum3, err := CalculateFileChecksum(path3)
	if err != nil {
		t.Fatalf("CalculateFileChecksum failed: %v", err)
	}

	if checksum == checksum3 {
		t.Error("different content produced same checksum")
	}
}

func TestCalculateFileChecksum_NotFound(t *testing.T) {
	_, err := CalculateFileChecksum("/nonexistent/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestGenerateAndParseBackupTimestamp(t *testing.T) {
	ts := GenerateBackupTimestamp()
	if ts == "" {
		t.Fatal("GenerateBackupTimestamp returned empty string")
	}

	parsed, err := ParseBackupTimestamp(ts)
	if err != nil {
		t.Fatalf("ParseBackupTimestamp failed: %v", err)
	}

	// Should be within the last minute
	if time.Since(parsed) > time.Minute {
		t.Errorf("parsed timestamp is too old: %v", parsed)
	}
}

func TestParseBackupTimestamp_Invalid(t *testing.T) {
	_, err := ParseBackupTimestamp("not-a-timestamp")
	if err == nil {
		t.Error("expected error for invalid timestamp, got nil")
	}
}
