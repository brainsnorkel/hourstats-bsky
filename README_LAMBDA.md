# Bluesky HourStats - AWS Lambda Serverless Architecture

This document describes the AWS Lambda serverless implementation of the Bluesky HourStats bot, designed for cost-effective, scalable operation.

## Architecture Overview

```
EventBridge (30min) → Lambda Function → SSM Parameters → Bluesky API
                           ↓
                    CloudWatch Logs
```

### Components:
- **EventBridge Rule**: Triggers every 30 minutes
- **Lambda Function**: Go runtime, processes trending posts
- **SSM Parameter Store**: Secure configuration management
- **CloudWatch Logs**: Centralized logging and monitoring

## Cost Analysis

### Monthly AWS Costs (us-east-1):
- **Lambda**: ~$0.50 (2 executions/hour × 24 hours × 30 days)
- **CloudWatch Logs**: ~$0.10 (14-day retention)
- **SSM Parameter Store**: ~$0.05 (5 parameters)
- **EventBridge**: $0.00 (first 1M events free)

**Total Estimated Cost**: ~$0.65/month

## Quick Start

### 1. Prerequisites
```bash
# Install required tools
go install github.com/aws/aws-lambda-go/lambda@latest
terraform --version  # >= 1.0
aws --version        # AWS CLI configured
```

### 2. Build and Deploy
```bash
# Build Lambda function
make -f Makefile.lambda build-lambda

# Deploy to AWS
make -f Makefile.lambda deploy-lambda

# Update SSM parameters
make -f Makefile.lambda update-params
```

### 3. Configure Parameters
```bash
# Set your Bluesky credentials
aws ssm put-parameter \
  --name "/hourstats/bluesky/handle" \
  --value "your-handle.bsky.social" \
  --type "String" \
  --overwrite

aws ssm put-parameter \
  --name "/hourstats/bluesky/password" \
  --value "your-app-password" \
  --type "SecureString" \
  --overwrite
```

## Project Structure

```
├── cmd/
│   ├── lambda/           # Lambda entry point
│   │   └── main.go
│   └── trendjournal/     # Original main (local dev)
├── internal/
│   ├── lambda/           # Lambda-specific code
│   │   ├── handler.go    # Main analysis logic
│   │   └── config.go     # SSM integration
│   ├── analyzer/         # Sentiment analysis
│   ├── client/           # Bluesky API client
│   ├── scheduler/        # Analysis orchestration
│   └── config/           # Configuration structures
├── terraform/            # Infrastructure as Code
│   └── main.tf
├── .github/workflows/    # CI/CD pipeline
│   └── deploy-lambda.yml
├── Makefile.lambda       # Lambda-specific commands
└── README_LAMBDA.md      # This file
```

## Configuration

### SSM Parameters
| Parameter | Type | Description | Default |
|-----------|------|-------------|---------|
| `/hourstats/bluesky/handle` | String | Bluesky handle | Required |
| `/hourstats/bluesky/password` | SecureString | App password | Required |
| `/hourstats/settings/analysis_interval_minutes` | String | Analysis interval | 30 |
| `/hourstats/settings/top_posts_count` | String | Number of top posts | 5 |
| `/hourstats/settings/min_engagement_score` | String | Min engagement | 10 |
| `/hourstats/settings/dry_run` | String | Enable dry run | false |

### Lambda Configuration
- **Runtime**: Go (provided.al2)
- **Memory**: 1024 MB
- **Timeout**: 15 minutes
- **Reserved Concurrency**: 1

## Development

### Local Development
```bash
# Run original version locally
go run cmd/trendjournal/main.go

# Test Lambda function locally
make -f Makefile.lambda test-lambda
```

### Testing
```bash
# Run all tests
go test ./...

# Test specific package
go test ./internal/lambda/...

# Run with coverage
go test -cover ./...
```

### Building
```bash
# Build for Lambda
make -f Makefile.lambda build-lambda

# Build for local development
go build -o hourstats cmd/trendjournal/main.go
```

## Deployment

### Manual Deployment
```bash
# Full deployment
make -f Makefile.lambda full-deploy

# Update only function code
make -f Makefile.lambda update-lambda

# Destroy everything
make -f Makefile.lambda full-destroy
```

### CI/CD Deployment
The GitHub Actions workflow automatically deploys on push to `main` branch:
1. Runs tests and linting
2. Builds Lambda function
3. Deploys with Terraform
4. Updates SSM parameters
5. Tests the deployed function

## Monitoring

### CloudWatch Logs
```bash
# View logs
make -f Makefile.lambda logs-lambda

# Or with AWS CLI
aws logs tail /aws/lambda/hourstats --follow
```

### CloudWatch Metrics
- **Duration**: Function execution time
- **Errors**: Function error count
- **Invocations**: Function invocation count
- **Throttles**: Function throttle count

### Alarms
- **High Error Rate**: >5% errors
- **Long Duration**: >10 minutes
- **Function Failures**: Any failure

## Troubleshooting

### Common Issues

#### 1. Authentication Errors
```bash
# Check SSM parameters
aws ssm get-parameters --names "/hourstats/bluesky/handle" "/hourstats/bluesky/password"

# Update credentials
aws ssm put-parameter --name "/hourstats/bluesky/handle" --value "new-handle" --overwrite
```

#### 2. Function Timeout
```bash
# Check function configuration
aws lambda get-function --function-name hourstats

# Increase timeout in terraform/main.tf
timeout = 900  # 15 minutes
```

#### 3. Memory Issues
```bash
# Check memory usage in CloudWatch
# Increase memory in terraform/main.tf
memory_size = 2048  # 2GB
```

### Debugging
```bash
# Invoke function manually
make -f Makefile.lambda invoke-lambda

# Check function status
make -f Makefile.lambda status-lambda

# View recent logs
aws logs describe-log-streams --log-group-name /aws/lambda/hourstats --order-by LastEventTime --descending --max-items 1
```

## Security

### IAM Permissions
The Lambda function has minimal required permissions:
- `ssm:GetParameter` - Read configuration
- `ssm:GetParameters` - Read multiple parameters
- `ssm:GetParametersByPath` - Read parameter hierarchy

### Parameter Security
- Sensitive parameters stored as `SecureString`
- Parameters encrypted with AWS KMS
- Access controlled via IAM policies

### Network Security
- Lambda runs in AWS managed VPC
- No inbound network access required
- Outbound HTTPS to Bluesky API only

## Performance Optimization

### Cold Start Mitigation
- Go runtime provides fast cold starts
- Reserved concurrency prevents cold starts
- Function stays warm with regular invocations

### Memory Optimization
- 1024MB memory for optimal performance
- Efficient Go memory management
- Minimal dependencies

### Cost Optimization
- 14-day log retention
- Reserved concurrency = 1
- Efficient error handling

## Migration from Server-based Architecture

### Key Changes
1. **Scheduler**: Changed from infinite loop to one-shot execution
2. **Configuration**: Moved from YAML files to SSM Parameter Store
3. **Authentication**: Re-authenticate on each invocation
4. **Logging**: Use CloudWatch Logs instead of file logs

### Migration Steps
1. Deploy Lambda infrastructure
2. Configure SSM parameters
3. Test with dry-run mode
4. Enable production mode
5. Decommission old server

## Support

### Documentation
- [AWS Lambda Go Runtime](https://docs.aws.amazon.com/lambda/latest/dg/golang-handler.html)
- [Terraform AWS Provider](https://registry.terraform.io/providers/hashicorp/aws/latest)
- [SSM Parameter Store](https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-parameter-store.html)

### Monitoring
- CloudWatch Dashboard for metrics
- CloudWatch Alarms for alerts
- SNS notifications for critical issues

This serverless architecture provides a cost-effective, scalable, and maintainable solution for the Bluesky HourStats bot while maintaining all current functionality.
