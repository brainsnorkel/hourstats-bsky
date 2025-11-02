package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Client wraps S3 operations for backup/restore
type S3Client struct {
	client *s3.Client
	bucket string
}

// NewS3Client creates a new S3 client
func NewS3Client(ctx context.Context, bucket string) (*S3Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &S3Client{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
	}, nil
}

// UploadFile uploads a file to S3
func (s *S3Client) UploadFile(ctx context.Context, key string, filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", filePath, err)
	}
	defer file.Close()

	// Get file size for progress tracking
	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}

	// Use multipart upload for files larger than 100MB
	if fileInfo.Size() > 100*1024*1024 {
		return s.uploadMultipart(ctx, key, file, fileInfo.Size())
	}

	// Simple upload for smaller files
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   file,
	})

	if err != nil {
		return fmt.Errorf("failed to upload file %s to s3://%s/%s: %w", filePath, s.bucket, key, err)
	}

	return nil
}

// uploadMultipart handles multipart uploads for large files
func (s *S3Client) uploadMultipart(ctx context.Context, key string, file *os.File, size int64) error {
	// Create multipart upload
	createResult, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to create multipart upload: %w", err)
	}

	uploadID := createResult.UploadId
	partSize := int64(100 * 1024 * 1024) // 100MB per part
	var completedParts []types.CompletedPart
	partNumber := int32(1)

	for offset := int64(0); offset < size; offset += partSize {
		end := offset + partSize
		if end > size {
			end = size
		}

		// Read part
		buffer := make([]byte, end-offset)
		n, readErr := file.ReadAt(buffer, offset)
		if readErr != nil && readErr != io.EOF {
			s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.bucket),
				Key:      aws.String(key),
				UploadId: uploadID,
			})
			return fmt.Errorf("failed to read file part: %w", readErr)
		}

		// Upload part (only upload the bytes we actually read)
		uploadResult, err := s.client.UploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(s.bucket),
			Key:        aws.String(key),
			PartNumber: aws.Int32(partNumber),
			UploadId:   uploadID,
			Body:       strings.NewReader(string(buffer[:n])),
		})
		if err != nil {
			s.client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(s.bucket),
				Key:      aws.String(key),
				UploadId: uploadID,
			})
			return fmt.Errorf("failed to upload part %d: %w", partNumber, err)
		}

		completedParts = append(completedParts, types.CompletedPart{
			ETag:       uploadResult.ETag,
			PartNumber: aws.Int32(partNumber),
		})

		partNumber++
	}

	// Complete multipart upload
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	return nil
}

// DownloadFile downloads a file from S3
func (s *S3Client) DownloadFile(ctx context.Context, key string, filePath string) error {
	// Create directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	result, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("failed to download file s3://%s/%s: %w", s.bucket, key, err)
	}
	defer result.Body.Close()

	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", filePath, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, result.Body); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filePath, err)
	}

	return nil
}

// ListObjects lists all objects with a given prefix
func (s *S3Client) ListObjects(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var continuationToken *string

	for {
		listInput := &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: continuationToken,
		}

		result, err := s.client.ListObjectsV2(ctx, listInput)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range result.Contents {
			keys = append(keys, *obj.Key)
		}

		if result.IsTruncated == nil || !*result.IsTruncated {
			break
		}

		continuationToken = result.NextContinuationToken
	}

	return keys, nil
}

// UploadDirectory uploads all files in a directory to S3 with a prefix
func (s *S3Client) UploadDirectory(ctx context.Context, localDir string, s3Prefix string) error {
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		// Calculate relative path from localDir
		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return fmt.Errorf("failed to get relative path: %w", err)
		}

		// Convert to S3 key format (use forward slashes)
		s3Key := s3Prefix + "/" + strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		if err := s.UploadFile(ctx, s3Key, path); err != nil {
			return fmt.Errorf("failed to upload %s: %w", path, err)
		}

		return nil
	})
}

// DownloadDirectory downloads all files from an S3 prefix to a local directory
func (s *S3Client) DownloadDirectory(ctx context.Context, s3Prefix string, localDir string) error {
	keys, err := s.ListObjects(ctx, s3Prefix)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}

	for _, key := range keys {
		// Skip directory markers
		if strings.HasSuffix(key, "/") {
			continue
		}

		// Calculate local file path
		relKey := strings.TrimPrefix(key, s3Prefix+"/")
		localPath := filepath.Join(localDir, relKey)

		if err := s.DownloadFile(ctx, key, localPath); err != nil {
			return fmt.Errorf("failed to download %s: %w", key, err)
		}
	}

	return nil
}

