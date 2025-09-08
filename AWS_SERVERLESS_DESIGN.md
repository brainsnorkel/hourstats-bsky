# AWS Serverless Architecture Design for Bluesky HourStats

## Overview

This document outlines the current AWS serverless architecture for the Bluesky HourStats bot, including the multi-Lambda workflow, DynamoDB state management, and the new 48-hour sentiment sparkline feature.

## Current Architecture

### Multi-Lambda Serverless Components:
- **EventBridge**: Triggers workflow every 30 minutes
- **Step Functions**: Orchestrates the multi-Lambda workflow
- **Orchestrator Lambda**: Manages run state and coordination
- **Fetcher Lambdas**: Collect posts from Bluesky API in parallel batches
- **Analyzer Lambda**: Performs sentiment analysis on collected posts
- **Aggregator Lambda**: Ranks posts and prepares final results
- **Poster Lambda**: Publishes main summary to Bluesky
- **Sparkline Poster Lambda**: Generates and posts 48-hour sentiment charts
- **DynamoDB**: State management and historical data storage
- **S3**: Storage for sparkline images
- **SSM Parameter Store**: Secure configuration management

### Key Benefits:
- **Scalability**: Handles unlimited posts without timeout issues
- **Cost Efficiency**: Pay only for actual compute time used
- **Reliability**: Each step can retry independently
- **Monitoring**: CloudWatch logs for each Lambda function
- **State Persistence**: DynamoDB ensures no data loss between steps

## 48-Hour Sentiment Sparkline Feature

### Overview
The system now includes a sophisticated 48-hour sentiment visualization feature that generates PNG sparkline charts and posts them to Bluesky with embedded images.

### Architecture Components

#### **Sparkline Poster Lambda**
- **Purpose**: Generates and posts 48-hour sentiment sparkline charts
- **Memory**: 256MB (increased for image processing)
- **Duration**: ~2 minutes
- **Dependencies**: Go graphics library (fogleman/gg)

#### **Historical Data Storage**
- **DynamoDB Table**: `hourstats-sentiment-history`
- **TTL**: 7 days (automatic cleanup)
- **Data Points**: Sentiment scores every 30 minutes
- **Storage**: ~1KB per data point

#### **Image Generation**
- **Library**: Go graphics library (fogleman/gg)
- **Format**: PNG images (400x200 pixels)
- **Features**: Line graphs with sentiment trends
- **Fallback**: Skips generation if insufficient data (<24 points)

#### **Image Storage**
- **S3 Bucket**: `hourstats-sparkline-images`
- **Access**: Public read access for Bluesky embedding
- **Lifecycle**: 30 days standard, then IA, then Glacier
- **Cost**: ~$0.50/month for current scale

### Data Flow
1. **Data Collection**: Each analysis run stores sentiment data in DynamoDB
2. **Chart Generation**: Sparkline Poster Lambda queries 48 hours of data
3. **Image Creation**: Go graphics library generates PNG sparkline
4. **S3 Upload**: Image uploaded to S3 with public read access
5. **Bluesky Post**: Image embedded directly in Bluesky post

### Cost Impact
- **Additional Lambda**: ~$2-5/month
- **DynamoDB Storage**: ~$1-2/month
- **S3 Storage**: ~$0.50/month
- **Total Additional Cost**: ~$3.50-7.50/month

## Current AWS Serverless Architecture

### 1. EventBridge Rule (Trigger)
```yaml
# CloudFormation/SAM Template
EventBridgeRule:
  Type: AWS::Events::Rule
  Properties:
    Name: hourstats-trigger
    Description: "Triggers Bluesky HourStats every 30 minutes"
    ScheduleExpression: "rate(30 minutes)"
    State: ENABLED
    Targets:
      - Arn: !GetAtt HourStatsLambda.Arn
        Id: "HourStatsTarget"
```

### 2. Lambda Function (Compute)
```go
// cmd/lambda/main.go
package main

import (
    "context"
    "github.com/aws/aws-lambda-go/lambda"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
    "github.com/aws/aws-sdk-go-v2/config"
)

type Event struct {
    Source string `json:"source"`
    Time   string `json:"time"`
}

func HandleRequest(ctx context.Context, event Event) (string, error) {
    // Load configuration from SSM Parameter Store
    cfg, err := loadConfigFromSSM(ctx)
    if err != nil {
        return "", err
    }
    
    // Initialize and run analysis
    analyzer := NewHourStatsAnalyzer(cfg)
    result, err := analyzer.RunAnalysis(ctx)
    if err != nil {
        return "", err
    }
    
    return result, nil
}

func main() {
    lambda.Start(HandleRequest)
}
```

### 3. SSM Parameter Store (Configuration)
```yaml
# Parameters to store in SSM
Parameters:
  /hourstats/bluesky/handle:
    Type: String
    Value: "hourstats.bsky.social"
    Description: "Bluesky handle for the bot"
  
  /hourstats/bluesky/password:
    Type: SecureString
    Value: "app-password-here"
    Description: "Bluesky app password"
  
  /hourstats/settings/analysis_interval_minutes:
    Type: String
    Value: "30"
    Description: "Analysis interval in minutes"
  
  /hourstats/settings/top_posts_count:
    Type: String
    Value: "5"
    Description: "Number of top posts to include"
  
  /hourstats/settings/dry_run:
    Type: String
    Value: "false"
    Description: "Enable dry run mode"
```

### 4. CloudWatch Logs (Logging)
```yaml
# CloudWatch Log Group
LogGroup:
  Type: AWS::Logs::LogGroup
  Properties:
    LogGroupName: "/aws/lambda/hourstats"
    RetentionInDays: 14  # Cost optimization
```

## Code Architecture Changes

### 1. New Lambda Handler Structure
```
cmd/
├── lambda/
│   └── main.go              # Lambda entry point
├── trendjournal/
│   └── main.go              # Original main (for local dev)
internal/
├── lambda/
│   ├── handler.go           # Lambda-specific logic
│   └── config.go            # SSM Parameter Store integration
├── analyzer/                # Unchanged
├── client/                  # Unchanged
└── scheduler/               # Modified for one-shot execution
```

### 2. Modified Scheduler for One-Shot Execution
```go
// internal/scheduler/scheduler.go
type Scheduler struct {
    client   *client.BlueskyClient
    analyzer *analyzer.SentimentAnalyzer
    config   *config.Config
}

// New method for one-shot execution
func (s *Scheduler) RunAnalysis(ctx context.Context) (*AnalysisResult, error) {
    // Authenticate with Bluesky
    if err := s.client.Authenticate(); err != nil {
        return nil, err
    }
    
    // Fetch trending posts
    clientPosts, err := s.client.GetTrendingPosts(s.config.Settings.AnalysisIntervalMinutes)
    if err != nil {
        return nil, err
    }
    
    // Analyze sentiment and extract topics
    analyzedPosts, err := s.analyzer.AnalyzePosts(analyzerPosts)
    if err != nil {
        return nil, err
    }
    
    // Get top 5 posts
    topPosts := s.GetTopPosts(analyzedPosts, 5)
    
    // Calculate overall sentiment
    overallSentiment := s.CalculateOverallSentiment(topPosts)
    
    // Post the results
    if err := s.client.PostTrendingSummary(clientTopPosts, overallSentiment, s.config.Settings.AnalysisIntervalMinutes); err != nil {
        return nil, err
    }
    
    return &AnalysisResult{
        PostsAnalyzed: len(analyzedPosts),
        TopPosts: len(topPosts),
        Sentiment: overallSentiment,
        Success: true,
    }, nil
}
```

### 3. SSM Parameter Store Integration
```go
// internal/lambda/config.go
package lambda

import (
    "context"
    "github.com/aws/aws-sdk-go-v2/service/ssm"
    "github.com/aws/aws-sdk-go-v2/config"
)

type SSMConfigLoader struct {
    client *ssm.Client
}

func NewSSMConfigLoader(ctx context.Context) (*SSMConfigLoader, error) {
    cfg, err := config.LoadDefaultConfig(ctx)
    if err != nil {
        return nil, err
    }
    
    return &SSMConfigLoader{
        client: ssm.NewFromConfig(cfg),
    }, nil
}

func (s *SSMConfigLoader) LoadConfig(ctx context.Context) (*config.Config, error) {
    // Load parameters from SSM
    params := map[string]string{
        "/hourstats/bluesky/handle": "",
        "/hourstats/bluesky/password": "",
        "/hourstats/settings/analysis_interval_minutes": "30",
        "/hourstats/settings/top_posts_count": "5",
        "/hourstats/settings/dry_run": "false",
    }
    
    for paramName := range params {
        result, err := s.client.GetParameter(ctx, &ssm.GetParameterInput{
            Name:           &paramName,
            WithDecryption: true,
        })
        if err != nil {
            return nil, err
        }
        params[paramName] = *result.Parameter.Value
    }
    
    // Convert to config struct
    return &config.Config{
        Bluesky: config.BlueskyConfig{
            Handle:   params["/hourstats/bluesky/handle"],
            Password: params["/hourstats/bluesky/password"],
        },
        Settings: config.SettingsConfig{
            AnalysisIntervalMinutes: parseInt(params["/hourstats/settings/analysis_interval_minutes"]),
            TopPostsCount:          parseInt(params["/hourstats/settings/top_posts_count"]),
            DryRun:                 params["/hourstats/settings/dry_run"] == "true",
        },
    }, nil
}
```

## Infrastructure as Code (Terraform)

### 1. Terraform Configuration
```hcl
# terraform/main.tf
provider "aws" {
  region = "us-east-1"
}

# Lambda Function
resource "aws_lambda_function" "hourstats" {
  filename         = "hourstats.zip"
  function_name    = "hourstats"
  role            = aws_iam_role.lambda_role.arn
  handler         = "main"
  source_code_hash = filebase64sha256("hourstats.zip")
  runtime         = "provided.al2"  # Go runtime
  timeout         = 900  # 15 minutes
  memory_size     = 1024  # 1GB
  
  environment {
    variables = {
      LOG_LEVEL = "INFO"
    }
  }
}

# EventBridge Rule
resource "aws_cloudwatch_event_rule" "hourstats_schedule" {
  name                = "hourstats-schedule"
  description         = "Trigger HourStats every 30 minutes"
  schedule_expression = "rate(30 minutes)"
}

resource "aws_cloudwatch_event_target" "hourstats_target" {
  rule      = aws_cloudwatch_event_rule.hourstats_schedule.name
  target_id = "HourStatsTarget"
  arn       = aws_lambda_function.hourstats.arn
}

# IAM Role for Lambda
resource "aws_iam_role" "lambda_role" {
  name = "hourstats-lambda-role"
  
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
}

# IAM Policy for SSM Parameter Store
resource "aws_iam_policy" "lambda_ssm_policy" {
  name = "hourstats-lambda-ssm-policy"
  
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
          "arn:aws:ssm:*:*:parameter/hourstats/*"
        ]
      }
    ]
  })
}

# Attach policies to role
resource "aws_iam_role_policy_attachment" "lambda_basic" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

resource "aws_iam_role_policy_attachment" "lambda_ssm" {
  role       = aws_iam_role.lambda_role.name
  policy_arn = aws_iam_policy.lambda_ssm_policy.arn
}
```

## Cost Optimization Strategies

### 1. Lambda Configuration
- **Memory**: 1024MB (optimal for Go runtime)
- **Timeout**: 15 minutes (max for EventBridge)
- **Runtime**: Go (fast cold start)
- **Reserved Concurrency**: 1 (prevent concurrent executions)

### 2. CloudWatch Logs
- **Retention**: 14 days (vs default 30 days)
- **Log Level**: INFO (reduce verbose logging)

### 3. SSM Parameter Store
- **Standard Tier**: For non-sensitive parameters
- **Advanced Tier**: For sensitive parameters (passwords)

### 4. EventBridge
- **Rate**: 30 minutes (vs current 1 hour)
- **No additional cost** for EventBridge rules

## Estimated Monthly Costs

### AWS Services (us-east-1):
- **Lambda**: ~$0.50 (2 executions/hour × 24 hours × 30 days × $0.0000166667/GB-second)
- **CloudWatch Logs**: ~$0.10 (minimal logging)
- **SSM Parameter Store**: ~$0.05 (5 parameters)
- **EventBridge**: $0.00 (first 1M events free)

**Total Estimated Cost**: ~$0.65/month

## Deployment Strategy

### 1. Build Process
```bash
# Build for Lambda
GOOS=linux GOARCH=amd64 go build -o main cmd/lambda/main.go
zip hourstats.zip main

# Deploy with Terraform
terraform init
terraform plan
terraform apply
```

### 2. CI/CD Pipeline
```yaml
# .github/workflows/deploy.yml
name: Deploy to AWS
on:
  push:
    branches: [main]
    
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v3
        with:
          go-version: '1.21'
      
      - name: Build Lambda
        run: |
          GOOS=linux GOARCH=amd64 go build -o main cmd/lambda/main.go
          zip hourstats.zip main
      
      - name: Deploy with Terraform
        run: |
          terraform init
          terraform plan
          terraform apply -auto-approve
```

## Migration Plan

### Phase 1: Preparation
1. Create Lambda handler structure
2. Implement SSM Parameter Store integration
3. Modify scheduler for one-shot execution
4. Add Terraform infrastructure code

### Phase 2: Testing
1. Deploy to AWS staging environment
2. Test with dry-run mode
3. Validate configuration loading
4. Test error handling and retries

### Phase 3: Production Migration
1. Deploy to production AWS account
2. Configure SSM parameters
3. Enable EventBridge rule
4. Monitor CloudWatch logs
5. Decommission old server

## Benefits of Serverless Architecture

### 1. Cost Efficiency
- Pay only for execution time
- No idle server costs
- Automatic scaling

### 2. Reliability
- Built-in retry mechanisms
- Dead letter queues
- CloudWatch monitoring

### 3. Maintenance
- No server management
- Automatic updates
- Built-in logging

### 4. Security
- IAM role-based access
- Encrypted parameter storage
- VPC integration possible

## Monitoring and Alerting

### 1. CloudWatch Metrics
- Lambda duration
- Error rate
- Throttle count
- Memory utilization

### 2. CloudWatch Alarms
- High error rate (>5%)
- Long duration (>10 minutes)
- Function failures

### 3. SNS Notifications
- Error alerts
- Performance warnings
- Daily summary reports

This serverless architecture provides a cost-effective, scalable, and maintainable solution for the Bluesky HourStats bot while maintaining all current functionality.
