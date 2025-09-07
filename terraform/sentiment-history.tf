# DynamoDB table for sentiment history
resource "aws_dynamodb_table" "sentiment_history" {
  name           = "hourstats-sentiment-history"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "runId"
  range_key      = "timestamp"

  attribute {
    name = "runId"
    type = "S"
  }

  attribute {
    name = "timestamp"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  global_secondary_index {
    name     = "timestamp-index"
    hash_key = "timestamp"
  }

  tags = {
    Name        = "HourStats Sentiment History"
    Environment = "production"
  }
}

# S3 bucket for sparkline images
resource "aws_s3_bucket" "sparkline_images" {
  bucket = "hourstats-sparkline-images"

  tags = {
    Name        = "HourStats Sparkline Images"
    Environment = "production"
  }
}

# S3 bucket versioning
resource "aws_s3_bucket_versioning" "sparkline_images" {
  bucket = aws_s3_bucket.sparkline_images.id
  versioning_configuration {
    status = "Enabled"
  }
}

# S3 bucket lifecycle configuration
resource "aws_s3_bucket_lifecycle_configuration" "sparkline_images" {
  bucket = aws_s3_bucket.sparkline_images.id

  rule {
    id     = "delete_old_images"
    status = "Enabled"

    expiration {
      days = 30
    }
  }
}

# S3 bucket public access block
resource "aws_s3_bucket_public_access_block" "sparkline_images" {
  bucket = aws_s3_bucket.sparkline_images.id

  block_public_acls       = false
  block_public_policy     = false
  ignore_public_acls      = false
  restrict_public_buckets = false
}

# S3 bucket policy for public read access to images
resource "aws_s3_bucket_policy" "sparkline_images" {
  bucket = aws_s3_bucket.sparkline_images.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "PublicReadGetObject"
        Effect    = "Allow"
        Principal = "*"
        Action    = "s3:GetObject"
        Resource  = "${aws_s3_bucket.sparkline_images.arn}/*"
      }
    ]
  })
}

# IAM policy for Lambda functions to access sentiment history
resource "aws_iam_policy" "sentiment_history_access" {
  name        = "HourStatsSentimentHistoryAccess"
  description = "Policy for HourStats Lambda functions to access sentiment history"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:Query",
          "dynamodb:Scan"
        ]
        Resource = [
          aws_dynamodb_table.sentiment_history.arn,
          "${aws_dynamodb_table.sentiment_history.arn}/index/*"
        ]
      }
    ]
  })
}

# IAM policy for Lambda functions to access S3
resource "aws_iam_policy" "s3_sparkline_access" {
  name        = "HourStatsS3SparklineAccess"
  description = "Policy for HourStats Lambda functions to access S3 for sparkline images"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "s3:GetObject",
          "s3:PutObject",
          "s3:DeleteObject"
        ]
        Resource = [
          "${aws_s3_bucket.sparkline_images.arn}/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "s3:ListBucket"
        ]
        Resource = [
          aws_s3_bucket.sparkline_images.arn
        ]
      }
    ]
  })
}
