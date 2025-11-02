package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Manifest represents backup metadata
type Manifest struct {
	BackupTimestamp string                `json:"backupTimestamp"`
	BackupVersion   string                `json:"backupVersion"`
	Tables          []TableManifest       `json:"tables"`
	TotalItems      int                   `json:"totalItems"`
	Checksum        string                `json:"checksum"`
}

// TableManifest contains metadata for a single table backup
type TableManifest struct {
	TableName      string   `json:"tableName"`
	ItemCount      int      `json:"itemCount"`
	FileSize       int64    `json:"fileSize"`
	FileName       string   `json:"fileName"`
	Checksum       string   `json:"checksum"`
	BackupDuration string   `json:"backupDuration"`
}

// WriteManifest writes the manifest to a file
func WriteManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// ReadManifest reads and parses a manifest file
func ReadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

// CalculateFileChecksum calculates SHA256 checksum of a file
func CalculateFileChecksum(filePath string) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file for checksum: %w", err)
	}

	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// GenerateBackupTimestamp generates a timestamp string for backup directory names
func GenerateBackupTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15-04-05Z")
}

// ParseBackupTimestamp parses a backup timestamp string
func ParseBackupTimestamp(ts string) (time.Time, error) {
	return time.Parse("2006-01-02T15-04-05Z", ts)
}

