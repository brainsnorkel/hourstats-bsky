# AWS Provider Configuration
terraform {
  required_version = ">= 1.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

# Variables
variable "aws_region" {
  description = "AWS region for resources"
  type        = string
  default     = "us-east-1"
}

variable "function_name" {
  description = "Name of the Lambda function"
  type        = string
  default     = "hourstats"
}

variable "schedule_expression" {
  description = "EventBridge schedule expression"
  type        = string
  default     = "rate(30 minutes)"
}

# Data sources
data "aws_caller_identity" "current" {}

# IAM Role for Lambda
resource "aws_iam_role" "lambda_role" {
  name = "${var.function_name}-lambda-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "lambda.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-lambda-role"
    Environment = "production"
  }
}

# IAM Policy for Lambda functions
resource "aws_iam_policy" "lambda_policy" {
  name        = "${var.function_name}-lambda-policy"
  description = "Policy for Lambda functions"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "logs:CreateLogGroup",
          "logs:CreateLogStream",
          "logs:PutLogEvents"
        ]
        Resource = "arn:aws:logs:*:*:*"
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:Query",
          "dynamodb:UpdateItem",
          "dynamodb:BatchWriteItem"
        ]
        Resource = aws_dynamodb_table.hourstats_state.arn
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:Query",
          "dynamodb:Scan"
        ]
        Resource = aws_dynamodb_table.sentiment_history.arn
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:Query",
          "dynamodb:Scan"
        ]
        Resource = "${aws_dynamodb_table.sentiment_history.arn}/index/*"
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:Query"
        ]
        Resource = "${aws_dynamodb_table.hourstats_state.arn}/index/*"
      },
      {
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters"
        ]
        Resource = [
          "arn:aws:ssm:${var.aws_region}:${data.aws_caller_identity.current.account_id}:parameter/hourstats/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "lambda:InvokeFunction"
        ]
        Resource = [
          aws_lambda_function.hourstats_orchestrator.arn,
          aws_lambda_function.hourstats_fetcher.arn,
          aws_lambda_function.hourstats_processor.arn,
          aws_lambda_function.hourstats_sparkline_poster.arn
        ]
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-lambda-policy"
    Environment = "production"
  }
}

# Attach policy to role
resource "aws_iam_role_policy_attachment" "lambda_policy" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = aws_iam_policy.lambda_policy.arn
}

# Attach sentiment history policy to role
resource "aws_iam_role_policy_attachment" "sentiment_history_policy" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = aws_iam_policy.sentiment_history_access.arn
}

# Note: S3 sparkline policy attachment removed - using embedded images instead

# DynamoDB Table for Multi-Lambda State Management
resource "aws_dynamodb_table" "hourstats_state" {
  name           = "hourstats-state"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "runId"
  range_key      = "postId"

  attribute {
    name = "runId"
    type = "S"
  }

  attribute {
    name = "postId"
    type = "S"
  }


  attribute {
    name = "status"
    type = "S"
  }

  attribute {
    name = "createdAt"
    type = "S"
  }

  # Global Secondary Index for querying by status
  global_secondary_index {
    name            = "status-index"
    hash_key        = "status"
    range_key       = "createdAt"
    projection_type = "ALL"
  }

  # Global Secondary Index for querying posts
  global_secondary_index {
    name               = "posts-index"
    hash_key           = "runId"
    range_key          = "postId"
    projection_type    = "INCLUDE"
    non_key_attributes = ["post", "posts", "createdAt", "ttl"]
  }

  # Global Secondary Index for efficient run listing
  global_secondary_index {
    name            = "runs-index"
    hash_key        = "runId"
    range_key       = "createdAt"
    projection_type = "ALL"
  }

  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name        = "${var.function_name}-state"
    Environment = "production"
  }
}

# Orchestrator Lambda Function
resource "aws_lambda_function" "hourstats_orchestrator" {
  filename         = "lambda-orchestrator.zip"
  function_name    = "hourstats-orchestrator"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-orchestrator.zip")
  runtime         = "provided.al2023"
  timeout         = 900  # 15 minutes
  memory_size     = 128

  environment {
    variables = {
      DYNAMODB_TABLE = aws_dynamodb_table.hourstats_state.name
    }
  }

  tags = {
    Name        = "${var.function_name}-orchestrator"
    Environment = "production"
  }
}

# Fetcher Lambda Function
resource "aws_lambda_function" "hourstats_fetcher" {
  filename         = "lambda-fetcher.zip"
  function_name    = "hourstats-fetcher"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-fetcher.zip")
  runtime         = "provided.al2023"
  timeout         = 900  # 15 minutes
  memory_size     = 128

  environment {
    variables = {
      DYNAMODB_TABLE = aws_dynamodb_table.hourstats_state.name
    }
  }

  tags = {
    Name        = "${var.function_name}-fetcher"
    Environment = "production"
  }
}

# Processor Lambda Function
resource "aws_lambda_function" "hourstats_processor" {
  filename         = "lambda-processor.zip"
  function_name    = "hourstats-processor"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-processor.zip")
  runtime         = "provided.al2023"
  timeout         = 300  # 5 minutes
  memory_size     = 128

  environment {
    variables = {
      DYNAMODB_TABLE = aws_dynamodb_table.hourstats_state.name
    }
  }

  tags = {
    Name        = "${var.function_name}-processor"
    Environment = "production"
  }
}

# Sparkline Poster Lambda Function
resource "aws_lambda_function" "hourstats_sparkline_poster" {
  filename         = "lambda-sparkline-poster.zip"
  function_name    = "hourstats-sparkline-poster"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-sparkline-poster.zip")
  runtime         = "provided.al2023"
  timeout         = 300  # 5 minutes
  memory_size     = 256

  environment {
    variables = {
      DYNAMODB_TABLE = aws_dynamodb_table.hourstats_state.name
      SENTIMENT_HISTORY_TABLE = aws_dynamodb_table.sentiment_history.name
    }
  }

  tags = {
    Name        = "${var.function_name}-sparkline-poster"
    Environment = "production"
  }
}

# EventBridge Rule
resource "aws_cloudwatch_event_rule" "hourstats_schedule" {
  name                = "${var.function_name}-schedule"
  description         = "Trigger ${var.function_name} on schedule"
  schedule_expression = var.schedule_expression

  tags = {
    Name        = "${var.function_name}-schedule"
    Environment = "production"
  }
}

# EventBridge Target to invoke Orchestrator Lambda
resource "aws_cloudwatch_event_target" "hourstats_target" {
  rule      = aws_cloudwatch_event_rule.hourstats_schedule.name
  target_id = "HourStatsTarget"
  arn       = aws_lambda_function.hourstats_orchestrator.arn

  input = jsonencode({
    source                  = "aws.events"
    time                    = "$.time"
    analysisIntervalMinutes = 30
  })
}

# Permission for EventBridge to invoke Orchestrator Lambda
resource "aws_lambda_permission" "allow_eventbridge_orchestrator" {
  statement_id  = "AllowExecutionFromEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.hourstats_orchestrator.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.hourstats_schedule.arn
}

# SSM Parameters
resource "aws_ssm_parameter" "bluesky_handle" {
  name  = "/hourstats/bluesky/handle"
  type  = "String"
  value = "your-handle.bsky.social"

  tags = {
    Name        = "${var.function_name}-bluesky-handle"
    Environment = "production"
  }
}

# Note: The bluesky password should be set manually via AWS CLI or Console
# This data source references the existing parameter without managing it
data "aws_ssm_parameter" "bluesky_password" {
  name = "/hourstats/bluesky/password"
}

# CloudWatch Log Groups are automatically created by AWS Lambda
# No need to define them explicitly in Terraform

# Outputs
output "dynamodb_table_name" {
  description = "Name of the DynamoDB table"
  value       = aws_dynamodb_table.hourstats_state.name
}

output "orchestrator_function_arn" {
  description = "ARN of the orchestrator Lambda function"
  value       = aws_lambda_function.hourstats_orchestrator.arn
}

output "fetcher_function_arn" {
  description = "ARN of the fetcher Lambda function"
  value       = aws_lambda_function.hourstats_fetcher.arn
}

output "processor_function_arn" {
  description = "ARN of the processor Lambda function"
  value       = aws_lambda_function.hourstats_processor.arn
}

output "eventbridge_rule_arn" {
  description = "ARN of the EventBridge rule"
  value       = aws_cloudwatch_event_rule.hourstats_schedule.arn
}

output "log_group_name" {
  description = "Name of the CloudWatch log group"
  value       = "/aws/lambda/hourstats-orchestrator"
}