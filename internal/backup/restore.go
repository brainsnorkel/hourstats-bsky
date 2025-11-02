package backup

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// RestoreOptions configures restore behavior
type RestoreOptions struct {
	InputPath    string
	S3Bucket     string
	S3Prefix     string
	Tables       []string
	ClearFirst   bool
	DryRun       bool
	ProgressFunc func(tableName string, itemsRestored int)
}

// RestoreResult contains information about a completed restore
type RestoreResult struct {
	TablesRestored int
	TotalItems     int
	Duration       time.Duration
	Errors         []string
}

// Restore restores data from a backup
func Restore(ctx context.Context, options RestoreOptions) (*RestoreResult, error) {
	// Download from S3 if specified
	restorePath := options.InputPath
	if options.S3Bucket != "" {
		tempDir, err := os.MkdirTemp("", "dynamodb-restore-*")
		if err != nil {
			return nil, fmt.Errorf("failed to create temp directory: %w", err)
		}
		defer os.RemoveAll(tempDir)

		s3Client, err := NewS3Client(ctx, options.S3Bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to create S3 client: %w", err)
		}

		log.Printf("Downloading backup from s3://%s/%s", options.S3Bucket, options.S3Prefix)
		if err := s3Client.DownloadDirectory(ctx, options.S3Prefix, tempDir); err != nil {
			return nil, fmt.Errorf("failed to download from S3: %w", err)
		}

		// Find the backup directory (should be the only subdirectory)
		entries, err := os.ReadDir(tempDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read temp directory: %w", err)
		}

		for _, entry := range entries {
			if entry.IsDir() {
				restorePath = filepath.Join(tempDir, entry.Name())
				break
			}
		}

		if restorePath == options.InputPath {
			return nil, fmt.Errorf("failed to find backup directory in S3 download")
		}
	}

	// Read manifest
	manifestPath := filepath.Join(restorePath, "manifest.json")
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	log.Printf("Restoring backup from %s (created: %s)", restorePath, manifest.BackupTimestamp)

	// Initialize DynamoDB client
	dbClient, err := NewDynamoDBClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create DynamoDB client: %w", err)
	}

	// Filter tables to restore
	tablesToRestore := options.Tables
	if len(tablesToRestore) == 0 {
		// Restore all tables in manifest
		for _, tableManifest := range manifest.Tables {
			tablesToRestore = append(tablesToRestore, tableManifest.TableName)
		}
	}

	result := &RestoreResult{
		Errors: []string{},
	}

	for _, tableName := range tablesToRestore {
		// Find table in manifest
		var tableManifest *TableManifest
		for _, tm := range manifest.Tables {
			if tm.TableName == tableName {
				tableManifest = &tm
				break
			}
		}

		if tableManifest == nil {
			errMsg := fmt.Sprintf("Table %s not found in backup manifest", tableName)
			result.Errors = append(result.Errors, errMsg)
			log.Printf("Warning: %s", errMsg)
			continue
		}

		// Read items from JSONL file
		filePath := filepath.Join(restorePath, tableManifest.FileName)
		items, err := readItemsJSONL(filePath, tableManifest.FileName)
		if err != nil {
			errMsg := fmt.Sprintf("Failed to read items for table %s: %v", tableName, err)
			result.Errors = append(result.Errors, errMsg)
			log.Printf("Error: %s", errMsg)
			continue
		}

		if options.DryRun {
			log.Printf("[DRY RUN] Would restore %d items to table %s", len(items), tableName)
			result.TotalItems += len(items)
			result.TablesRestored++
			continue
		}

		// Clear table if requested
		if options.ClearFirst {
			log.Printf("Warning: ClearFirst option not implemented - items will be merged with existing data")
		}

		// Track progress
		itemsRestored := 0
		progressFunc := func(count int) {
			itemsRestored += count
			if options.ProgressFunc != nil {
				options.ProgressFunc(tableName, itemsRestored)
			}
		}

		// Write items to DynamoDB
		if err := dbClient.BatchWriteItems(ctx, tableName, items, progressFunc); err != nil {
			errMsg := fmt.Sprintf("Failed to restore items to table %s: %v", tableName, err)
			result.Errors = append(result.Errors, errMsg)
			log.Printf("Error: %s", errMsg)
			continue
		}

		result.TablesRestored++
		result.TotalItems += len(items)
		log.Printf("Successfully restored %d items to table %s", len(items), tableName)
	}

	return result, nil
}

// readItemsJSONL reads items from a JSONL file (or gzip-compressed JSONL)
func readItemsJSONL(filePath string, fileName string) ([]map[string]types.AttributeValue, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	var scanner *bufio.Scanner
	var items []map[string]types.AttributeValue

	// Check if file is gzip compressed
	if strings.HasSuffix(fileName, ".gz") {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzipReader.Close()
		scanner = bufio.NewScanner(gzipReader)
	} else {
		scanner = bufio.NewScanner(file)
	}

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()

		var item map[string]types.AttributeValue
		if err := json.Unmarshal(line, &item); err != nil {
			log.Printf("Warning: failed to parse item on line %d: %v", lineNum, err)
			continue
		}

		items = append(items, item)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return items, nil
}

