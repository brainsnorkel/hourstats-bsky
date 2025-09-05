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

# IAM Policy for SSM Parameter Store and DynamoDB
resource "aws_iam_policy" "lambda_ssm_policy" {
  name        = "${var.function_name}-lambda-ssm-policy"
  description = "Policy for Lambda to access SSM Parameter Store and DynamoDB"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "ssm:GetParameter",
          "ssm:GetParameters",
          "ssm:GetParametersByPath"
        ]
        Resource = [
          "arn:aws:ssm:${var.aws_region}:${data.aws_caller_identity.current.account_id}:parameter/hourstats/*"
        ]
      },
      {
        Effect = "Allow"
        Action = [
          "dynamodb:GetItem",
          "dynamodb:PutItem",
          "dynamodb:UpdateItem",
          "dynamodb:DeleteItem",
          "dynamodb:Query",
          "dynamodb:Scan"
        ]
        Resource = [
          aws_dynamodb_table.hourstats_state.arn,
          "${aws_dynamodb_table.hourstats_state.arn}/index/*"
        ]
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-lambda-ssm-policy"
    Environment = "production"
  }
}

# Attach basic execution role
resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# Attach SSM policy
resource "aws_iam_role_policy_attachment" "lambda_ssm" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = aws_iam_policy.lambda_ssm_policy.arn
}

# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "lambda_logs" {
  name              = "/aws/lambda/${var.function_name}"
  retention_in_days = 14

  tags = {
    Name        = "${var.function_name}-logs"
    Environment = "production"
  }
}

# Lambda Function
resource "aws_lambda_function" "hourstats" {
  filename         = "hourstats.zip"
  function_name    = var.function_name
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("hourstats.zip")
  runtime         = "provided.al2"
  timeout         = 900  # 15 minutes (function takes ~12 min, keeping safe margin)
  memory_size     = 256  # 256MB (function only uses 84MB max, 3x headroom)

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = var.function_name
    Environment = "production"
  }
}

# Step Functions State Machine
resource "aws_sfn_state_machine" "hourstats_workflow" {
  name     = "${var.function_name}-workflow"
  role_arn = aws_iam_role.step_functions_role.arn

  definition = templatefile("${path.module}/step-functions-definition.json", {
    aws_region    = var.aws_region
    aws_account_id = data.aws_caller_identity.current.account_id
  })

  tags = {
    Name        = "${var.function_name}-workflow"
    Environment = "production"
  }
}

# IAM Role for Step Functions
resource "aws_iam_role" "step_functions_role" {
  name = "${var.function_name}-step-functions-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "states.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-step-functions-role"
    Environment = "production"
  }
}

# IAM Policy for Step Functions to invoke Lambda functions
resource "aws_iam_policy" "step_functions_policy" {
  name        = "${var.function_name}-step-functions-policy"
  description = "Policy for Step Functions to invoke Lambda functions"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "lambda:InvokeFunction"
        ]
        Resource = [
          "arn:aws:lambda:${var.aws_region}:${data.aws_caller_identity.current.account_id}:function:hourstats-*"
        ]
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-step-functions-policy"
    Environment = "production"
  }
}

# Attach policy to role
resource "aws_iam_role_policy_attachment" "step_functions_policy" {
  role       = aws_iam_role.step_functions_role.name
  policy_arn = aws_iam_policy.step_functions_policy.arn
}

# Individual Lambda Functions for Multi-Lambda Architecture
resource "aws_lambda_function" "orchestrator" {
  filename         = "lambda-orchestrator.zip"
  function_name    = "${var.function_name}-orchestrator"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-orchestrator.zip")
  runtime         = "provided.al2"
  timeout         = 60   # 1 minute
  memory_size     = 128  # 128MB

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = "${var.function_name}-orchestrator"
    Environment = "production"
  }
}

resource "aws_lambda_function" "fetcher" {
  filename         = "lambda-fetcher.zip"
  function_name    = "${var.function_name}-fetcher"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-fetcher.zip")
  runtime         = "provided.al2"
  timeout         = 300  # 5 minutes
  memory_size     = 256  # 256MB

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = "${var.function_name}-fetcher"
    Environment = "production"
  }
}

resource "aws_lambda_function" "analyzer" {
  filename         = "lambda-analyzer.zip"
  function_name    = "${var.function_name}-analyzer"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-analyzer.zip")
  runtime         = "provided.al2"
  timeout         = 180  # 3 minutes
  memory_size     = 256  # 256MB

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = "${var.function_name}-analyzer"
    Environment = "production"
  }
}

resource "aws_lambda_function" "aggregator" {
  filename         = "lambda-aggregator.zip"
  function_name    = "${var.function_name}-aggregator"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-aggregator.zip")
  runtime         = "provided.al2"
  timeout         = 60   # 1 minute
  memory_size     = 128  # 128MB

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = "${var.function_name}-aggregator"
    Environment = "production"
  }
}

resource "aws_lambda_function" "poster" {
  filename         = "lambda-poster.zip"
  function_name    = "${var.function_name}-poster"
  role            = aws_iam_role.lambda_role.arn
  handler         = "bootstrap"
  source_code_hash = filebase64sha256("lambda-poster.zip")
  runtime         = "provided.al2"
  timeout         = 60   # 1 minute
  memory_size     = 128  # 128MB

  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.lambda_logs,
    aws_iam_role_policy_attachment.lambda_basic,
    aws_iam_role_policy_attachment.lambda_ssm,
  ]

  tags = {
    Name        = "${var.function_name}-poster"
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

# EventBridge Target to invoke Step Functions workflow
resource "aws_cloudwatch_event_target" "hourstats_target" {
  rule      = aws_cloudwatch_event_rule.hourstats_schedule.name
  target_id = "HourStatsTarget"
  arn       = aws_sfn_state_machine.hourstats_workflow.arn
  role_arn  = aws_iam_role.eventbridge_step_functions_role.arn
}

# IAM Role for EventBridge to invoke Step Functions
resource "aws_iam_role" "eventbridge_step_functions_role" {
  name = "${var.function_name}-eventbridge-step-functions-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRole"
        Effect = "Allow"
        Principal = {
          Service = "events.amazonaws.com"
        }
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-eventbridge-step-functions-role"
    Environment = "production"
  }
}

# IAM Policy for EventBridge to invoke Step Functions
resource "aws_iam_policy" "eventbridge_step_functions_policy" {
  name        = "${var.function_name}-eventbridge-step-functions-policy"
  description = "Policy for EventBridge to invoke Step Functions"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "states:StartExecution"
        ]
        Resource = [
          aws_sfn_state_machine.hourstats_workflow.arn
        ]
      }
    ]
  })

  tags = {
    Name        = "${var.function_name}-eventbridge-step-functions-policy"
    Environment = "production"
  }
}

# Attach policy to role
resource "aws_iam_role_policy_attachment" "eventbridge_step_functions_policy" {
  role       = aws_iam_role.eventbridge_step_functions_role.name
  policy_arn = aws_iam_policy.eventbridge_step_functions_policy.arn
}

# SSM Parameters
resource "aws_ssm_parameter" "bluesky_handle" {
  name  = "/hourstats/bluesky/handle"
  type  = "String"
  value = "hourstats.bsky.social"
  overwrite = true

  tags = {
    Name        = "hourstats-bluesky-handle"
    Environment = "production"
  }
}

# Note: /hourstats/bluesky/password is managed manually via AWS CLI or console
# This parameter should be created manually with:
# aws ssm put-parameter --name "/hourstats/bluesky/password" --value "your-password" --type "SecureString" --region us-east-1

resource "aws_ssm_parameter" "analysis_interval" {
  name  = "/hourstats/settings/analysis_interval_minutes"
  type  = "String"
  value = "30"
  overwrite = true

  tags = {
    Name        = "hourstats-analysis-interval"
    Environment = "production"
  }
}

resource "aws_ssm_parameter" "top_posts_count" {
  name  = "/hourstats/settings/top_posts_count"
  type  = "String"
  value = "5"
  overwrite = true

  tags = {
    Name        = "hourstats-top-posts-count"
    Environment = "production"
  }
}

resource "aws_ssm_parameter" "min_engagement_score" {
  name  = "/hourstats/settings/min_engagement_score"
  type  = "String"
  value = "10"
  overwrite = true

  tags = {
    Name        = "hourstats-min-engagement-score"
    Environment = "production"
  }
}

resource "aws_ssm_parameter" "dry_run" {
  name  = "/hourstats/settings/dry_run"
  type  = "String"
  value = "false"
  overwrite = true

  tags = {
    Name        = "hourstats-dry-run"
    Environment = "production"
  }
}

# CloudWatch Alarms
resource "aws_cloudwatch_metric_alarm" "lambda_errors" {
  alarm_name          = "${var.function_name}-errors"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "Errors"
  namespace           = "AWS/Lambda"
  period              = "300"
  statistic           = "Sum"
  threshold           = "0"
  alarm_description   = "This metric monitors lambda errors"
  alarm_actions       = []

  dimensions = {
    FunctionName = aws_lambda_function.hourstats.function_name
  }

  tags = {
    Name        = "${var.function_name}-errors-alarm"
    Environment = "production"
  }
}

resource "aws_cloudwatch_metric_alarm" "lambda_duration" {
  alarm_name          = "${var.function_name}-duration"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "Duration"
  namespace           = "AWS/Lambda"
  period              = "300"
  statistic           = "Average"
  threshold           = "600000" # 10 minutes in milliseconds
  alarm_description   = "This metric monitors lambda duration"
  alarm_actions       = []

  dimensions = {
    FunctionName = aws_lambda_function.hourstats.function_name
  }

  tags = {
    Name        = "${var.function_name}-duration-alarm"
    Environment = "production"
  }
}

# DynamoDB Table for Multi-Lambda State Management
resource "aws_dynamodb_table" "hourstats_state" {
  name           = "${var.function_name}-state"
  billing_mode   = "PAY_PER_REQUEST"
  hash_key       = "runId"
  range_key      = "step"

  attribute {
    name = "runId"
    type = "S"
  }

  attribute {
    name = "step"
    type = "S"
  }

  # Global Secondary Index for querying by status
  global_secondary_index {
    name     = "status-index"
    hash_key = "status"
    range_key = "createdAt"
    projection_type = "ALL"
  }

  attribute {
    name = "status"
    type = "S"
  }

  attribute {
    name = "createdAt"
    type = "S"
  }

  # TTL for automatic cleanup of old runs (7 days)
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  tags = {
    Name        = "${var.function_name}-state"
    Environment = "production"
  }
}

# Outputs
output "lambda_function_name" {
  description = "Name of the Lambda function"
  value       = aws_lambda_function.hourstats.function_name
}

output "lambda_function_arn" {
  description = "ARN of the Lambda function"
  value       = aws_lambda_function.hourstats.arn
}

output "eventbridge_rule_arn" {
  description = "ARN of the EventBridge rule"
  value       = aws_cloudwatch_event_rule.hourstats_schedule.arn
}

output "log_group_name" {
  description = "Name of the CloudWatch log group"
  value       = aws_cloudwatch_log_group.lambda_logs.name
}
# Test
