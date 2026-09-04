package store

import (
	"context"
	"database/sql"
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

// essentialTables are the tables that contain irreplaceable data.
// Transient tables (post_buffer, topic_tokens, cursor) are regenerated
// from the live firehose and skipped to keep backups small and fast.
var essentialTables = []string{
	"runs",
	"sentiment_history",
	"daily_sentiment",
	"topic_snapshots",
	"topic_identity",
	"key_value",
	// 400-day report rollups; unreconstructable once topic_snapshots and
	// runs have been purged.
	"topic_daily",
	"daily_top_post",
}

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

	slog.Info("starting essential-tables backup", "dest", backupPath, "tables", len(essentialTables))

	if err := s.backupEssentialTables(ctx, backupPath); err != nil {
		return "", err
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		return "", fmt.Errorf("stat backup: %w", err)
	}

	slog.Info("backup complete", "path", backupPath, "size_bytes", info.Size())

	pruned, err := pruneBackups(backupDir, profile, retainDays)
	if err != nil {
		slog.Warn("backup pruning failed", "error", err)
	} else if pruned > 0 {
		slog.Info("pruned old backups", "count", pruned)
	}

	return backupPath, nil
}

func (s *Store) BackupToWriter(ctx context.Context, w io.Writer) error {
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("hourstats-backup-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpPath)

	if err := s.backupEssentialTables(ctx, tmpPath); err != nil {
		return err
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

type S3BackupConfig struct {
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Profile         string
}

func (s *Store) BackupToS3(ctx context.Context, cfg S3BackupConfig) (string, error) {
	ts := time.Now().UTC().Format(backupTimeFormat)
	key := fmt.Sprintf("%s/%s.db", cfg.Profile, ts)

	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("hourstats-s3-%d.db", time.Now().UnixNano()))
	defer os.Remove(tmpPath)

	if err := s.backupEssentialTables(ctx, tmpPath); err != nil {
		return "", err
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		return "", fmt.Errorf("open temp backup: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat temp backup: %w", err)
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

	s3Client := s3.NewFromConfig(awsCfg)
	_, err = s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(cfg.Bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	if err != nil {
		return "", fmt.Errorf("s3 put: %w", err)
	}

	slog.Info("backup uploaded to S3", "bucket", cfg.Bucket, "key", key, "size_bytes", info.Size())
	return fmt.Sprintf("s3://%s/%s", cfg.Bucket, key), nil
}

// backupEssentialTables creates a new SQLite DB at destPath containing only
// the irreplaceable tables. Opens a separate connection to the source and
// uses ATTACH to copy data — no VACUUM INTO, so the main DB's WAL writer
// (firehose ingestion) is never blocked.
func (s *Store) backupEssentialTables(ctx context.Context, destPath string) error {
	destDB, err := sql.Open("sqlite", destPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open dest db: %w", err)
	}
	defer destDB.Close()

	attachSQL := fmt.Sprintf(`ATTACH DATABASE 'file:%s?mode=ro' AS src`, s.dbPath)
	if _, err := destDB.ExecContext(ctx, attachSQL); err != nil {
		return fmt.Errorf("attach source db: %w", err)
	}

	for _, table := range essentialTables {
		createSQL := `CREATE TABLE IF NOT EXISTS main.` + table + ` AS SELECT * FROM src.` + table + ` WHERE 0`
		if _, err := destDB.ExecContext(ctx, createSQL); err != nil {
			return fmt.Errorf("create table %s: %w", table, err)
		}

		insertSQL := `INSERT INTO main.` + table + ` SELECT * FROM src.` + table
		if _, err := destDB.ExecContext(ctx, insertSQL); err != nil {
			return fmt.Errorf("copy table %s: %w", table, err)
		}
	}

	if _, err := destDB.ExecContext(ctx, `DETACH DATABASE src`); err != nil {
		return fmt.Errorf("detach source db: %w", err)
	}

	return nil
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
