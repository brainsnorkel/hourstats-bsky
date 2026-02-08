package store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	backupSubdir      = "backups"
	backupTimeFormat  = "2006-01-02T150405Z"
	defaultRetainDays = 7
)

// Backup creates a consistent point-in-time copy of the database using
// SQLite's VACUUM INTO, which produces a standalone, defragmented backup
// file that is safe to copy while the database is open and in WAL mode.
//
// Backups are written to <dataDir>/backups/hourstats-<profile>-<timestamp>.db
// and old backups beyond retainDays are pruned.
func (s *Store) Backup(ctx context.Context, dataDir, profile string, retainDays int) (string, error) {
	if retainDays <= 0 {
		retainDays = defaultRetainDays
	}

	backupDir := filepath.Join(dataDir, backupSubdir)
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	ts := time.Now().UTC().Format(backupTimeFormat)
	filename := fmt.Sprintf("hourstats-%s-%s.db", profile, ts)
	backupPath := filepath.Join(backupDir, filename)

	slog.Info("starting database backup", "dest", backupPath)

	// VACUUM INTO creates an atomic, consistent snapshot — works safely
	// while the main DB is open and handles WAL checkpointing internally.
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, backupPath))
	if err != nil {
		return "", fmt.Errorf("vacuum into: %w", err)
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		return "", fmt.Errorf("stat backup: %w", err)
	}

	slog.Info("backup complete",
		"path", backupPath,
		"size_bytes", info.Size(),
	)

	pruned, err := pruneBackups(backupDir, profile, retainDays)
	if err != nil {
		slog.Warn("backup pruning failed", "error", err)
	} else if pruned > 0 {
		slog.Info("pruned old backups", "count", pruned)
	}

	return backupPath, nil
}

// BackupToWriter creates a backup and writes it to w, then removes the
// temporary file. Useful for streaming backups to remote storage.
func (s *Store) BackupToWriter(ctx context.Context, w io.Writer) error {
	tmpDir := os.TempDir()
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("hourstats-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpPath)

	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, tmpPath))
	if err != nil {
		return fmt.Errorf("vacuum into temp: %w", err)
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open temp backup: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("copy backup: %w", err)
	}
	return nil
}

// S3BackupConfig holds the configuration for S3 backup uploads.
type S3BackupConfig struct {
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Profile         string
}

// BackupToS3 creates a point-in-time backup and uploads it to S3.
// The S3 key follows the pattern: <profile>/<timestamp>.db
func (s *Store) BackupToS3(ctx context.Context, cfg S3BackupConfig) (string, error) {
	ts := time.Now().UTC().Format(backupTimeFormat)
	key := fmt.Sprintf("%s/%s.db", cfg.Profile, ts)

	var buf bytes.Buffer
	if err := s.BackupToWriter(ctx, &buf); err != nil {
		return "", fmt.Errorf("backup to buffer: %w", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, "",
		)),
	)
	if err != nil {
		return "", fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(buf.Bytes()),
	})
	if err != nil {
		return "", fmt.Errorf("s3 put: %w", err)
	}

	slog.Info("backup uploaded to S3",
		"bucket", cfg.Bucket,
		"key", key,
		"size_bytes", buf.Len(),
	)
	return fmt.Sprintf("s3://%s/%s", cfg.Bucket, key), nil
}

func pruneBackups(dir, profile string, retainDays int) (int, error) {
	prefix := fmt.Sprintf("hourstats-%s-", profile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read backup dir: %w", err)
	}

	type backupEntry struct {
		name string
		ts   time.Time
	}

	var backups []backupEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		tsStr := strings.TrimPrefix(e.Name(), prefix)
		tsStr = strings.TrimSuffix(tsStr, ".db")
		ts, err := time.Parse(backupTimeFormat, tsStr)
		if err != nil {
			continue
		}
		backups = append(backups, backupEntry{name: e.Name(), ts: ts})
	}

	sort.Slice(backups, func(i, j int) bool {
		return backups[i].ts.Before(backups[j].ts)
	})

	cutoff := time.Now().UTC().AddDate(0, 0, -retainDays)
	pruned := 0
	for _, b := range backups {
		if b.ts.Before(cutoff) {
			path := filepath.Join(dir, b.name)
			if err := os.Remove(path); err != nil {
				slog.Warn("failed to remove old backup", "path", path, "error", err)
				continue
			}
			pruned++
		}
	}

	return pruned, nil
}
