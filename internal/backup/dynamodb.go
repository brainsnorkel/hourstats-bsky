package backup

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoDBClient wraps DynamoDB operations for backup/restore
type DynamoDBClient struct {
	client *dynamodb.Client
}

// NewDynamoDBClient creates a new DynamoDB client
func NewDynamoDBClient(ctx context.Context) (*DynamoDBClient, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &DynamoDBClient{
		client: dynamodb.NewFromConfig(cfg),
	}, nil
}

// ScanTable scans all items from a DynamoDB table with pagination
func (d *DynamoDBClient) ScanTable(ctx context.Context, tableName string, progressFunc func(int)) ([]map[string]types.AttributeValue, error) {
	var allItems []map[string]types.AttributeValue
	var lastEvaluatedKey map[string]types.AttributeValue
	scanCount := 0

	for {
		scanInput := &dynamodb.ScanInput{
			TableName: aws.String(tableName),
		}

		if lastEvaluatedKey != nil {
			scanInput.ExclusiveStartKey = lastEvaluatedKey
		}

		result, err := d.client.Scan(ctx, scanInput)
		if err != nil {
			return nil, fmt.Errorf("failed to scan table %s: %w", tableName, err)
		}

		allItems = append(allItems, result.Items...)
		scanCount++
		
		if progressFunc != nil {
			progressFunc(len(result.Items))
		}

		log.Printf("Scanned %d items from %s (total so far: %d)", len(result.Items), tableName, len(allItems))

		// Check if there are more items to scan
		if result.LastEvaluatedKey == nil || len(result.LastEvaluatedKey) == 0 {
			break
		}

		lastEvaluatedKey = result.LastEvaluatedKey

		// Small delay to avoid throttling
		time.Sleep(50 * time.Millisecond)
	}

	log.Printf("Completed scan of %s: %d total items in %d pages", tableName, len(allItems), scanCount)
	return allItems, nil
}

// BatchWriteItems writes items to a DynamoDB table in batches
// DynamoDB allows max 25 items per batch write
func (d *DynamoDBClient) BatchWriteItems(ctx context.Context, tableName string, items []map[string]types.AttributeValue, progressFunc func(int)) error {
	const batchSize = 25
	totalBatches := (len(items) + batchSize - 1) / batchSize
	writtenCount := 0

	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}

		batch := items[i:end]
		batchNum := (i / batchSize) + 1

		writeRequests := make([]types.WriteRequest, len(batch))
		for j, item := range batch {
			writeRequests[j] = types.WriteRequest{
				PutRequest: &types.PutRequest{
					Item: item,
				},
			}
		}

		batchInput := &dynamodb.BatchWriteItemInput{
			RequestItems: map[string][]types.WriteRequest{
				tableName: writeRequests,
			},
		}

		// Retry logic for throttling
		var err error
		maxRetries := 5
		for retry := 0; retry < maxRetries; retry++ {
			result, batchErr := d.client.BatchWriteItem(ctx, batchInput)
			if batchErr != nil {
				err = batchErr
				// Retry on any error (throttling or temporary failures)
				if retry < maxRetries-1 {
					backoff := time.Duration(retry+1) * 500 * time.Millisecond
					log.Printf("Error detected, retrying in %v (batch %d/%d): %v", backoff, batchNum, totalBatches, batchErr)
					time.Sleep(backoff)
					continue
				}
				return fmt.Errorf("failed to batch write to table %s (batch %d/%d): %w", tableName, batchNum, totalBatches, batchErr)
			}

			// Check for unprocessed items (throttling)
			if unprocessed := result.UnprocessedItems; len(unprocessed) > 0 {
				if retry < maxRetries-1 {
					log.Printf("Some items unprocessed, retrying (batch %d/%d)", batchNum, totalBatches)
					batchInput.RequestItems = unprocessed
					backoff := time.Duration(retry+1) * 500 * time.Millisecond
					time.Sleep(backoff)
					continue
				}
				log.Printf("Warning: %d items unprocessed after %d retries (batch %d/%d)", len(unprocessed[tableName]), maxRetries, batchNum, totalBatches)
			}

			writtenCount += len(batch)
			if progressFunc != nil {
				progressFunc(len(batch))
			}

			log.Printf("Wrote batch %d/%d: %d items to %s (total: %d/%d)", batchNum, totalBatches, len(batch), tableName, writtenCount, len(items))
			break
		}

		if err != nil {
			return err
		}

		// Small delay between batches to avoid throttling
		if i+batchSize < len(items) {
			time.Sleep(100 * time.Millisecond)
		}
	}

	log.Printf("Completed batch write to %s: %d total items in %d batches", tableName, writtenCount, totalBatches)
	return nil
}

// GetTableDescription retrieves table schema information
func (d *DynamoDBClient) GetTableDescription(ctx context.Context, tableName string) (*types.TableDescription, error) {
	result, err := d.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(tableName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to describe table %s: %w", tableName, err)
	}

	return result.Table, nil
}

// ConvertToDynamoDBItem converts a Go struct to DynamoDB attribute map
func ConvertToDynamoDBItem(item interface{}) (map[string]types.AttributeValue, error) {
	return attributevalue.MarshalMap(item)
}

// ConvertFromDynamoDBItem converts a DynamoDB attribute map to Go struct
func ConvertFromDynamoDBItem(av map[string]types.AttributeValue, out interface{}) error {
	return attributevalue.UnmarshalMap(av, out)
}

