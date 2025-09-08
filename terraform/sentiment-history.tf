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

# Note: S3 bucket removed - using embedded images via Bluesky blob service instead

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

# Note: S3 access policy removed - using embedded images via Bluesky blob service instead
