package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/christophergentle/hourstats-bsky/internal/backup"
)

func main() {
	var (
		tablesStr  = flag.String("tables", "", "Comma-separated list of table names to backup (required)")
		outputDir  = flag.String("output", "./backups", "Output directory for backups")
		s3Bucket   = flag.String("s3-bucket", "", "S3 bucket name (optional, if provided backup will be uploaded)")
		s3Prefix   = flag.String("s3-prefix", "hourstats-backup", "S3 prefix for backup files")
		compress   = flag.Bool("compress", false, "Compress backup files with gzip")
		verbose    = flag.Bool("verbose", false, "Enable verbose logging")
	)
	flag.Parse()

	if *tablesStr == "" {
		fmt.Fprintf(os.Stderr, "Error: --tables is required\n")
		flag.Usage()
		os.Exit(1)
	}

	// Parse table names
	tables := strings.Split(*tablesStr, ",")
	for i, table := range tables {
		tables[i] = strings.TrimSpace(table)
	}

	ctx := context.Background()

	// Setup progress function
	var progressFunc func(string, int)
	if *verbose {
		progressFunc = func(tableName string, itemsBackedUp int) {
			if itemsBackedUp%100 == 0 {
				log.Printf("Progress: %s - %d items backed up", tableName, itemsBackedUp)
			}
		}
	}

	options := backup.BackupOptions{
		Tables:       tables,
		OutputDir:    *outputDir,
		S3Bucket:     *s3Bucket,
		S3Prefix:     *s3Prefix,
		Compress:     *compress,
		ProgressFunc: progressFunc,
	}

	fmt.Printf("Starting backup of %d table(s)...\n", len(tables))
	fmt.Printf("Tables: %s\n", strings.Join(tables, ", "))
	fmt.Printf("Output: %s\n", *outputDir)
	if *s3Bucket != "" {
		fmt.Printf("S3: s3://%s/%s\n", *s3Bucket, *s3Prefix)
	}
	fmt.Println()

	result, err := backup.Backup(ctx, options)
	if err != nil {
		log.Fatalf("Backup failed: %v", err)
	}

	fmt.Println()
	fmt.Printf("Backup completed successfully!\n")
	fmt.Printf("  Backup path: %s\n", result.BackupPath)
	fmt.Printf("  Tables backed up: %d\n", result.TablesBackedUp)
	fmt.Printf("  Total items: %d\n", result.TotalItems)
	fmt.Printf("  Duration: %v\n", result.Duration.Round(time.Second))

	// Print table details
	fmt.Println()
	fmt.Println("Table details:")
	for _, table := range result.Manifest.Tables {
		fmt.Printf("  %s: %d items, %s file size, checksum: %s\n",
			table.TableName,
			table.ItemCount,
			formatBytes(table.FileSize),
			table.Checksum[:16]+"...")
	}
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

