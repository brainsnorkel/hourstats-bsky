# DynamoDB table for daily sentiment averages
resource "aws_dynamodb_table" "daily_sentiment" {
  name           = "hourstats-daily-sentiment"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "date"
  range_key      = "runId"

  attribute {
    name = "date"
    type = "S"
  }

  attribute {
    name = "runId"
    type = "S"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  global_secondary_index {
    name               = "date-index"
    hash_key           = "date"
    range_key          = "createdAt"
    projection_type    = "ALL"
  }

  global_secondary_index {
    name               = "sentiment-range-index"
    hash_key           = "sentimentRange"
    range_key          = "date"
    projection_type    = "INCLUDE"
    non_key_attributes = ["averageSentiment", "minSentiment", "maxSentiment"]
  }

  tags = {
    Name        = "HourStats Daily Sentiment"
    Environment = "production"
  }
}

# IAM policy for Lambda functions to access daily sentiment table
resource "aws_iam_policy" "daily_sentiment_access" {
  name        = "HourStatsDailySentimentAccess"
  description = "Policy for HourStats Lambda functions to access daily sentiment table"

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
          aws_dynamodb_table.daily_sentiment.arn,
          "${aws_dynamodb_table.daily_sentiment.arn}/index/*"
        ]
      }
    ]
  })
}

# Daily Aggregator Lambda Function
resource "aws_lambda_function" "hourstats_daily_aggregator" {
  filename         = "lambda-daily-aggregator.zip"
  function_name    = "hourstats-daily-aggregator"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-daily-aggregator.zip")
  runtime         = "provided.al2023"
  timeout         = 300  # 5 minutes
  memory_size     = 256

  environment {
    variables = {
      DAILY_SENTIMENT_TABLE = aws_dynamodb_table.daily_sentiment.name
      SENTIMENT_HISTORY_TABLE = aws_dynamodb_table.sentiment_history.name
    }
  }

  tags = {
    Name        = "hourstats-daily-aggregator"
    Environment = "production"
  }
}

# Yearly Poster Lambda Function
resource "aws_lambda_function" "hourstats_yearly_poster" {
  filename         = "lambda-yearly-poster.zip"
  function_name    = "hourstats-yearly-poster"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-yearly-poster.zip")
  runtime         = "provided.al2023"
  timeout         = 600  # 10 minutes
  memory_size     = 512

  environment {
    variables = {
      DAILY_SENTIMENT_TABLE = aws_dynamodb_table.daily_sentiment.name
      SENTIMENT_HISTORY_TABLE = aws_dynamodb_table.sentiment_history.name
    }
  }

  tags = {
    Name        = "hourstats-yearly-poster"
    Environment = "production"
  }
}

# EventBridge Rule for Daily Aggregation (runs daily at midnight UTC)
resource "aws_cloudwatch_event_rule" "daily_aggregation_schedule" {
  name                = "hourstats-daily-aggregation-schedule"
  description         = "Trigger daily sentiment aggregation at midnight UTC"
  schedule_expression = "cron(0 0 * * ? *)"

  tags = {
    Name        = "hourstats-daily-aggregation-schedule"
    Environment = "production"
  }
}

# EventBridge Target for Daily Aggregation
resource "aws_cloudwatch_event_target" "daily_aggregation_target" {
  rule      = aws_cloudwatch_event_rule.daily_aggregation_schedule.name
  target_id = "DailyAggregationTarget"
  arn       = aws_lambda_function.hourstats_daily_aggregator.arn

  input = jsonencode({
    source = "aws.events"
    time   = "$.time"
  })
}

# EventBridge Rule for Yearly Posting (runs monthly on 1st at 1:00 AM UTC)
resource "aws_cloudwatch_event_rule" "yearly_posting_schedule" {
  name                = "hourstats-yearly-posting-schedule"
  description         = "Trigger yearly sentiment posting on 1st of month at 1:00 AM UTC"
  schedule_expression = "cron(0 1 1 * ? *)"

  tags = {
    Name        = "hourstats-yearly-posting-schedule"
    Environment = "production"
  }
}

# EventBridge Target for Yearly Posting
resource "aws_cloudwatch_event_target" "yearly_posting_target" {
  rule      = aws_cloudwatch_event_rule.yearly_posting_schedule.name
  target_id = "YearlyPostingTarget"
  arn       = aws_lambda_function.hourstats_yearly_poster.arn

  input = jsonencode({
    source = "aws.events"
    time   = "$.time"
  })
}

# Permission for EventBridge to invoke Daily Aggregator Lambda
resource "aws_lambda_permission" "allow_eventbridge_daily_aggregator" {
  statement_id  = "AllowExecutionFromEventBridgeDailyAggregator"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.hourstats_daily_aggregator.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.daily_aggregation_schedule.arn
}

# Permission for EventBridge to invoke Yearly Poster Lambda
resource "aws_lambda_permission" "allow_eventbridge_yearly_poster" {
  statement_id  = "AllowExecutionFromEventBridgeYearlyPoster"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.hourstats_yearly_poster.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.yearly_posting_schedule.arn
}
