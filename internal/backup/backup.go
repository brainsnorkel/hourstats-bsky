package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// BackupOptions configures backup behavior
type BackupOptions struct {
	Tables         []string
	OutputDir      string
	S3Bucket       string
	S3Prefix       string
	Compress       bool
	ProgressFunc   func(tableName string, itemsBackedUp int)
}

// BackupResult contains information about a completed backup
type BackupResult struct {
	Manifest       Manifest
	BackupPath     string
	TablesBackedUp int
	TotalItems     int
	Duration       time.Duration
}

// Backup performs a backup of DynamoDB tables
func Backup(ctx context.Context, options BackupOptions) (*BackupResult, error) {
	startTime := time.Now()

	// Create backup directory
	timestamp := GenerateBackupTimestamp()
	backupDir := filepath.Join(options.OutputDir, fmt.Sprintf("backup-%s", timestamp))

	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create backup directory: %w", err)
	}

	log.Printf("Starting backup to %s", backupDir)

	// Initialize DynamoDB client
	dbClient, err := NewDynamoDBClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create DynamoDB client: %w", err)
	}

	manifest := Manifest{
		BackupTimestamp: timestamp,
		BackupVersion:   "1.0",
		Tables:          []TableManifest{},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var backupErr error

	// Backup each table (can be done in parallel)
	for _, tableName := range options.Tables {
		wg.Add(1)
		go func(tblName string) {
			defer wg.Done()

			tableStartTime := time.Now()
			log.Printf("Starting backup of table: %s", tblName)

			// Track progress
			itemsCounted := 0
			progressFunc := func(count int) {
				itemsCounted += count
				if options.ProgressFunc != nil {
					options.ProgressFunc(tblName, itemsCounted)
				}
			}

			// Scan all items from table
			items, err := dbClient.ScanTable(ctx, tblName, progressFunc)
			if err != nil {
				mu.Lock()
				backupErr = fmt.Errorf("failed to backup table %s: %w", tblName, err)
				mu.Unlock()
				return
			}

			// Write items to JSONL file
			fileName := fmt.Sprintf("%s.jsonl", tblName)
			if options.Compress {
				fileName = fileName + ".gz"
			}
			filePath := filepath.Join(backupDir, fileName)

			var fileSize int64
			if options.Compress {
				fileSize, err = writeItemsJSONLCompressed(filePath, items)
			} else {
				fileSize, err = writeItemsJSONL(filePath, items)
			}
			if err != nil {
				mu.Lock()
				backupErr = fmt.Errorf("failed to write items for table %s: %w", tblName, err)
				mu.Unlock()
				return
			}

			// Calculate checksum
			checksum, err := CalculateFileChecksum(filePath)
			if err != nil {
				log.Printf("Warning: failed to calculate checksum for %s: %v", tblName, err)
				checksum = ""
			}

			// Get table description
			tableDesc, err := dbClient.GetTableDescription(ctx, tblName)
			var metadata *types.TableDescription
			if err != nil {
				log.Printf("Warning: failed to get table description for %s: %v", tblName, err)
			} else {
				metadata = tableDesc
			}

			// Write table metadata
			metadataFile := filepath.Join(backupDir, fmt.Sprintf("%s.metadata.json", tblName))
			if err := writeTableMetadata(metadataFile, metadata); err != nil {
				log.Printf("Warning: failed to write metadata for %s: %v", tblName, err)
			}

			duration := time.Since(tableStartTime)
			tableManifest := TableManifest{
				TableName:      tblName,
				ItemCount:      len(items),
				FileSize:       fileSize,
				FileName:       fileName,
				Checksum:       checksum,
				BackupDuration: duration.String(),
			}

			mu.Lock()
			manifest.Tables = append(manifest.Tables, tableManifest)
			manifest.TotalItems += len(items)
			mu.Unlock()

			log.Printf("Completed backup of table %s: %d items in %v", tblName, len(items), duration)
		}(tableName)
	}

	wg.Wait()

	if backupErr != nil {
		return nil, backupErr
	}

	// Write manifest
	manifestPath := filepath.Join(backupDir, "manifest.json")
	if err := WriteManifest(manifestPath, manifest); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	// Calculate overall checksum
	manifestChecksum, err := CalculateFileChecksum(manifestPath)
	if err != nil {
		log.Printf("Warning: failed to calculate manifest checksum: %v", err)
	} else {
		manifest.Checksum = manifestChecksum
		// Update manifest with checksum
		if err := WriteManifest(manifestPath, manifest); err != nil {
			log.Printf("Warning: failed to update manifest with checksum: %v", err)
		}
	}

	duration := time.Since(startTime)

	result := &BackupResult{
		Manifest:       manifest,
		BackupPath:     backupDir,
		TablesBackedUp: len(options.Tables),
		TotalItems:     manifest.TotalItems,
		Duration:       duration,
	}

	log.Printf("Backup completed: %d tables, %d items in %v", result.TablesBackedUp, result.TotalItems, duration)

	// Upload to S3 if specified
	if options.S3Bucket != "" {
		s3Client, err := NewS3Client(ctx, options.S3Bucket)
		if err != nil {
			return nil, fmt.Errorf("failed to create S3 client: %w", err)
		}

		s3Prefix := options.S3Prefix
		if s3Prefix == "" {
			s3Prefix = "hourstats-backup"
		}
		s3Prefix = fmt.Sprintf("%s/backup-%s", s3Prefix, timestamp)

		log.Printf("Uploading backup to s3://%s/%s", options.S3Bucket, s3Prefix)
		if err := s3Client.UploadDirectory(ctx, backupDir, s3Prefix); err != nil {
			return nil, fmt.Errorf("failed to upload to S3: %w", err)
		}

		log.Printf("Successfully uploaded backup to S3")
	}

	return result, nil
}

// writeItemsJSONL writes items to a JSONL file (one item per line)
func writeItemsJSONL(filePath string, items []map[string]types.AttributeValue) (int64, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	var totalSize int64

	for _, item := range items {
		// Convert DynamoDB attribute map to JSON
		itemJSON, err := json.Marshal(item)
		if err != nil {
			return totalSize, fmt.Errorf("failed to marshal item: %w", err)
		}

		if _, err := file.Write(itemJSON); err != nil {
			return totalSize, fmt.Errorf("failed to write item: %w", err)
		}
		if _, err := file.WriteString("\n"); err != nil {
			return totalSize, fmt.Errorf("failed to write newline: %w", err)
		}

		totalSize += int64(len(itemJSON) + 1)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return totalSize, nil
	}

	return fileInfo.Size(), nil
}

// writeItemsJSONLCompressed writes items to a gzip-compressed JSONL file
func writeItemsJSONLCompressed(filePath string, items []map[string]types.AttributeValue) (int64, error) {
	file, err := os.Create(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// Use compress/gzip for compression
	gzipWriter := gzip.NewWriter(file)

	encoder := json.NewEncoder(gzipWriter)

	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			gzipWriter.Close()
			return 0, fmt.Errorf("failed to encode item: %w", err)
		}
	}

	if err := gzipWriter.Close(); err != nil {
		return 0, fmt.Errorf("failed to close gzip writer: %w", err)
	}

	fileInfo, err := file.Stat()
	if err != nil {
		return 0, nil
	}

	return fileInfo.Size(), nil
}

// writeTableMetadata writes table schema information to a JSON file
func writeTableMetadata(filePath string, tableDesc *types.TableDescription) error {
	if tableDesc == nil {
		return nil
	}

	// Create a simplified metadata structure
	metadata := map[string]interface{}{
		"tableName":      aws.ToString(tableDesc.TableName),
		"itemCount":      aws.ToInt64(tableDesc.ItemCount),
		"tableSizeBytes": aws.ToInt64(tableDesc.TableSizeBytes),
		"keySchema":      tableDesc.KeySchema,
	}

	if tableDesc.GlobalSecondaryIndexes != nil {
		metadata["globalSecondaryIndexes"] = tableDesc.GlobalSecondaryIndexes
	}

	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write metadata file: %w", err)
	}

	return nil
}

