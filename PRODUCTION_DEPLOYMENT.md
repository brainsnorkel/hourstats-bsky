# Production Deployment Guide - Bluesky HourStats Lambda

This guide walks you through deploying the Bluesky HourStats bot to AWS Lambda in production.

## Prerequisites

### 1. AWS Account Setup
- AWS Account with appropriate permissions
- AWS CLI configured with credentials
- Terraform installed (>= 1.0)
- Go installed (>= 1.21)

### 2. Required Tools
```bash
# Install AWS CLI
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# Install Terraform
wget https://releases.hashicorp.com/terraform/1.5.0/terraform_1.5.0_linux_amd64.zip
unzip terraform_1.5.0_linux_amd64.zip
sudo mv terraform /usr/local/bin/

# Verify installations
aws --version
terraform --version
go version
```

### 3. AWS Credentials
```bash
# Configure AWS CLI
aws configure
# Enter your Access Key ID, Secret Access Key, Region (us-east-1), and output format (json)
```

## Step-by-Step Deployment

### Step 1: Prepare Your Environment

```bash
# Clone the repository
git clone https://github.com/brainsnorkel/hourstats-bsky.git
cd hourstats-bsky

# Switch to develop branch
git checkout develop

# Install Go dependencies
go mod tidy
go mod download
```

### Step 2: Configure AWS Credentials

```bash
# Verify AWS access
aws sts get-caller-identity

# Create IAM user for deployment (if needed)
aws iam create-user --user-name hourstats-deploy
aws iam attach-user-policy --user-name hourstats-deploy --policy-arn arn:aws:iam::aws:policy/AdministratorAccess
aws iam create-access-key --user-name hourstats-deploy
```

### Step 3: Set Up SSM Parameters

```bash
# Set your Bluesky credentials
aws ssm put-parameter \
  --name "/hourstats/bluesky/handle" \
  --value "your-handle.bsky.social" \
  --type "String" \
  --description "Bluesky handle for the bot"

aws ssm put-parameter \
  --name "/hourstats/bluesky/password" \
  --value "your-app-password" \
  --type "SecureString" \
  --description "Bluesky app password"

# Set configuration parameters
aws ssm put-parameter \
  --name "/hourstats/settings/analysis_interval_minutes" \
  --value "30" \
  --type "String" \
  --description "Analysis interval in minutes"

aws ssm put-parameter \
  --name "/hourstats/settings/top_posts_count" \
  --value "5" \
  --type "String" \
  --description "Number of top posts to include"

aws ssm put-parameter \
  --name "/hourstats/settings/min_engagement_score" \
  --value "10" \
  --type "String" \
  --description "Minimum engagement score"

aws ssm put-parameter \
  --name "/hourstats/settings/dry_run" \
  --value "true" \
  --type "String" \
  --description "Enable dry run mode for testing"
```

### Step 4: Build and Deploy Infrastructure

```bash
# Build the Lambda function
make -f Makefile.lambda build-lambda

# Initialize Terraform
cd terraform
terraform init

# Review the deployment plan
terraform plan

# Deploy the infrastructure
terraform apply
```

### Step 5: Test the Deployment

```bash
# Test the Lambda function
make -f Makefile.lambda invoke-lambda

# Check function status
make -f Makefile.lambda status-lambda

# View logs
make -f Makefile.lambda logs-lambda
```

### Step 6: Configure Production Settings

```bash
# Disable dry run mode
aws ssm put-parameter \
  --name "/hourstats/settings/dry_run" \
  --value "false" \
  --type "String" \
  --overwrite

# Test with real posting
make -f Makefile.lambda invoke-lambda
```

### Step 7: Monitor and Verify

```bash
# Check CloudWatch logs
aws logs tail /aws/lambda/hourstats --follow

# Verify EventBridge rule
aws events describe-rule --name hourstats-schedule

# Check SSM parameters
aws ssm get-parameters --names "/hourstats/bluesky/handle" "/hourstats/settings/dry_run"
```

## Production Checklist

### ✅ Pre-Deployment
- [ ] AWS account configured
- [ ] Required tools installed
- [ ] Bluesky credentials obtained
- [ ] SSM parameters set
- [ ] Terraform plan reviewed

### ✅ Deployment
- [ ] Lambda function built
- [ ] Infrastructure deployed
- [ ] Function tested in dry-run mode
- [ ] Dry-run mode disabled
- [ ] Production mode verified

### ✅ Post-Deployment
- [ ] EventBridge rule active
- [ ] CloudWatch logs working
- [ ] Function executing on schedule
- [ ] Posts appearing on Bluesky
- [ ] Monitoring alerts configured

## Troubleshooting

### Common Issues

#### 1. Authentication Errors
```bash
# Check SSM parameters
aws ssm get-parameters --names "/hourstats/bluesky/handle" "/hourstats/bluesky/password"

# Verify credentials
aws ssm get-parameter --name "/hourstats/bluesky/handle" --with-decryption
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

#### 4. EventBridge Not Triggering
```bash
# Check rule status
aws events describe-rule --name hourstats-schedule

# Check target configuration
aws events list-targets-by-rule --rule hourstats-schedule
```

### Debugging Commands

```bash
# View recent logs
aws logs describe-log-streams --log-group-name /aws/lambda/hourstats --order-by LastEventTime --descending --max-items 1

# Get function configuration
aws lambda get-function-configuration --function-name hourstats

# List EventBridge rules
aws events list-rules --name-prefix hourstats

# Check SSM parameters
aws ssm get-parameters-by-path --path "/hourstats" --recursive
```

## Monitoring Setup

### CloudWatch Dashboard
```bash
# Create dashboard
aws cloudwatch put-dashboard --dashboard-name "HourStats" --dashboard-body '{
  "widgets": [
    {
      "type": "metric",
      "properties": {
        "metrics": [
          ["AWS/Lambda", "Invocations", "FunctionName", "hourstats"],
          ["AWS/Lambda", "Errors", "FunctionName", "hourstats"],
          ["AWS/Lambda", "Duration", "FunctionName", "hourstats"]
        ],
        "period": 300,
        "stat": "Sum",
        "region": "us-east-1",
        "title": "HourStats Lambda Metrics"
      }
    }
  ]
}'
```

### CloudWatch Alarms
```bash
# High error rate alarm
aws cloudwatch put-metric-alarm \
  --alarm-name "hourstats-high-error-rate" \
  --alarm-description "High error rate for HourStats Lambda" \
  --metric-name Errors \
  --namespace AWS/Lambda \
  --statistic Sum \
  --period 300 \
  --threshold 5 \
  --comparison-operator GreaterThanThreshold \
  --evaluation-periods 2 \
  --dimensions Name=FunctionName,Value=hourstats

# Long duration alarm
aws cloudwatch put-metric-alarm \
  --alarm-name "hourstats-long-duration" \
  --alarm-description "Long duration for HourStats Lambda" \
  --metric-name Duration \
  --namespace AWS/Lambda \
  --statistic Average \
  --period 300 \
  --threshold 600000 \
  --comparison-operator GreaterThanThreshold \
  --evaluation-periods 2 \
  --dimensions Name=FunctionName,Value=hourstats
```

## Security Considerations

### IAM Permissions
The Lambda function uses minimal required permissions:
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

## Cost Optimization

### Current Configuration
- **Lambda**: 1024MB memory, 15min timeout
- **Logs**: 14-day retention
- **Parameters**: 5 standard parameters
- **EventBridge**: 1 rule

### Estimated Monthly Cost
- **Lambda**: ~$0.50
- **CloudWatch Logs**: ~$0.10
- **SSM Parameter Store**: ~$0.05
- **EventBridge**: $0.00
- **Total**: ~$0.65/month

## Maintenance

### Regular Tasks
- Monitor CloudWatch logs for errors
- Check function performance metrics
- Update SSM parameters as needed
- Review and rotate credentials

### Updates
```bash
# Update function code
make -f Makefile.lambda update-lambda

# Update infrastructure
cd terraform
terraform plan
terraform apply
```

### Backup
- SSM parameters are automatically backed up
- CloudWatch logs retained for 14 days
- Terraform state stored in S3 (recommended)

## Support

### Documentation
- [AWS Lambda Go Runtime](https://docs.aws.amazon.com/lambda/latest/dg/golang-handler.html)
- [Terraform AWS Provider](https://registry.terraform.io/providers/hashicorp/aws/latest)
- [SSM Parameter Store](https://docs.aws.amazon.com/systems-manager/latest/userguide/systems-manager-parameter-store.html)

### Monitoring
- CloudWatch Dashboard for metrics
- CloudWatch Alarms for alerts
- SNS notifications for critical issues

This production deployment guide ensures a smooth transition from the current server-based architecture to the cost-effective AWS Lambda serverless solution.
