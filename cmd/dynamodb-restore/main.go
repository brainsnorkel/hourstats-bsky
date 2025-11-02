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
		inputPath  = flag.String("input", "", "Input path to backup directory (required if not using S3)")
		s3Bucket   = flag.String("s3-bucket", "", "S3 bucket name (optional, if provided backup will be downloaded from S3)")
		s3Prefix   = flag.String("s3-prefix", "", "S3 prefix for backup files (required if using S3)")
		tablesStr  = flag.String("tables", "", "Comma-separated list of table names to restore (empty = restore all tables)")
		clearFirst = flag.Bool("clear-first", false, "Clear table before restore (WARNING: this deletes existing data)")
		dryRun     = flag.Bool("dry-run", false, "Dry run mode - show what would be restored without actually restoring")
		verbose    = flag.Bool("verbose", false, "Enable verbose logging")
	)
	flag.Parse()

	if *inputPath == "" && *s3Bucket == "" {
		fmt.Fprintf(os.Stderr, "Error: either --input or --s3-bucket must be provided\n")
		flag.Usage()
		os.Exit(1)
	}

	if *s3Bucket != "" && *s3Prefix == "" {
		fmt.Fprintf(os.Stderr, "Error: --s3-prefix is required when using --s3-bucket\n")
		flag.Usage()
		os.Exit(1)
	}

	ctx := context.Background()

	// Parse table names if provided
	var tables []string
	if *tablesStr != "" {
		tableList := strings.Split(*tablesStr, ",")
		for _, table := range tableList {
			tables = append(tables, strings.TrimSpace(table))
		}
	}

	if *dryRun {
		fmt.Println("DRY RUN MODE - No changes will be made")
		fmt.Println()
	}

	if *clearFirst {
		fmt.Println("WARNING: ClearFirst is set - existing data may be overwritten")
		fmt.Println()
	}

	// Setup progress function
	var progressFunc func(string, int)
	if *verbose {
		progressFunc = func(tableName string, itemsRestored int) {
			if itemsRestored%100 == 0 {
				log.Printf("Progress: %s - %d items restored", tableName, itemsRestored)
			}
		}
	}

	options := backup.RestoreOptions{
		InputPath:    *inputPath,
		S3Bucket:     *s3Bucket,
		S3Prefix:     *s3Prefix,
		Tables:       tables,
		ClearFirst:   *clearFirst,
		DryRun:       *dryRun,
		ProgressFunc: progressFunc,
	}

	source := *inputPath
	if *s3Bucket != "" {
		source = fmt.Sprintf("s3://%s/%s", *s3Bucket, *s3Prefix)
	}

	fmt.Printf("Starting restore from %s...\n", source)
	if len(tables) > 0 {
		fmt.Printf("Tables to restore: %s\n", strings.Join(tables, ", "))
	} else {
		fmt.Println("Tables to restore: all tables in backup")
	}
	fmt.Println()

	result, err := backup.Restore(ctx, options)
	if err != nil {
		log.Fatalf("Restore failed: %v", err)
	}

	fmt.Println()
	if *dryRun {
		fmt.Printf("Dry run completed!\n")
	} else {
		fmt.Printf("Restore completed successfully!\n")
	}
	fmt.Printf("  Tables restored: %d\n", result.TablesRestored)
	fmt.Printf("  Total items: %d\n", result.TotalItems)
	fmt.Printf("  Duration: %v\n", result.Duration.Round(time.Second))

	if len(result.Errors) > 0 {
		fmt.Println()
		fmt.Println("Errors encountered:")
		for _, err := range result.Errors {
			fmt.Printf("  - %s\n", err)
		}
		os.Exit(1)
	}
}

