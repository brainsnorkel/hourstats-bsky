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

# IAM Policy for SSM Parameter Store
resource "aws_iam_policy" "lambda_ssm_policy" {
  name        = "${var.function_name}-lambda-ssm-policy"
  description = "Policy for Lambda to access SSM Parameter Store"

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

# EventBridge Target
resource "aws_cloudwatch_event_target" "hourstats_target" {
  rule      = aws_cloudwatch_event_rule.hourstats_schedule.name
  target_id = "HourStatsTarget"
  arn       = aws_lambda_function.hourstats.arn
}

# Lambda Permission for EventBridge
resource "aws_lambda_permission" "allow_eventbridge" {
  statement_id  = "AllowExecutionFromEventBridge"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.hourstats.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.hourstats_schedule.arn
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
