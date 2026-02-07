# S3 Bucket for DynamoDB Backups
resource "aws_s3_bucket" "dynamodb_backups" {
  bucket = "hourstats-dynamodb-backups"

  tags = {
    Name        = "HourStats DynamoDB Backups"
    Environment = "production"
    Purpose     = "dynamodb-backup-storage"
  }
}

resource "aws_s3_bucket_versioning" "dynamodb_backups" {
  bucket = aws_s3_bucket.dynamodb_backups.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "dynamodb_backups" {
  bucket = aws_s3_bucket.dynamodb_backups.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "dynamodb_backups" {
  bucket = aws_s3_bucket.dynamodb_backups.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_lifecycle_configuration" "dynamodb_backups" {
  bucket = aws_s3_bucket.dynamodb_backups.id

  rule {
    id     = "expire-old-backups"
    status = "Enabled"

    filter {}

    expiration {
      days = 90
    }

    noncurrent_version_expiration {
      noncurrent_days = 30
    }
  }
}

output "backup_bucket_name" {
  description = "Name of the DynamoDB backup S3 bucket"
  value       = aws_s3_bucket.dynamodb_backups.bucket
}
