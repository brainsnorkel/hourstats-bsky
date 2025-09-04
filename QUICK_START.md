# Quick Start - Deploy to Production in 5 Minutes

This guide gets your Bluesky HourStats bot running on AWS Lambda in under 5 minutes.

## Prerequisites (2 minutes)

### 1. Install Required Tools
```bash
# Install AWS CLI (if not already installed)
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install

# Install Terraform (if not already installed)
wget https://releases.hashicorp.com/terraform/1.5.0/terraform_1.5.0_linux_amd64.zip
unzip terraform_1.5.0_linux_amd64.zip
sudo mv terraform /usr/local/bin/

# Verify installations
aws --version
terraform --version
go version
```

### 2. Configure AWS Credentials
```bash
aws configure
# Enter your Access Key ID, Secret Access Key, Region (us-east-1), and output format (json)
```

## Deploy (3 minutes)

### Option 1: Automated Deployment (Recommended)
```bash
# Run the automated deployment script
./scripts/deploy-production.sh
```

### Option 2: Manual Deployment
```bash
# 1. Set your Bluesky credentials
aws ssm put-parameter --name "/hourstats/bluesky/handle" --value "your-handle.bsky.social" --type "String" --overwrite
aws ssm put-parameter --name "/hourstats/bluesky/password" --value "your-app-password" --type "SecureString" --overwrite

# 2. Set configuration
aws ssm put-parameter --name "/hourstats/settings/analysis_interval_minutes" --value "30" --type "String" --overwrite
aws ssm put-parameter --name "/hourstats/settings/dry_run" --value "true" --type "String" --overwrite

# 3. Build and deploy
make -f Makefile.lambda build-lambda
cd terraform
terraform init
terraform apply -auto-approve
cd ..

# 4. Test
make -f Makefile.lambda invoke-lambda
```

## Verify Deployment

### Check Function Status
```bash
aws lambda get-function --function-name hourstats
```

### View Logs
```bash
aws logs tail /aws/lambda/hourstats --follow
```

### Test Function
```bash
make -f Makefile.lambda invoke-lambda
```

## Enable Production Mode

Once you've verified everything works in dry-run mode:

```bash
# Disable dry run to start posting
aws ssm put-parameter --name "/hourstats/settings/dry_run" --value "false" --type "String" --overwrite
```

## Monitor Your Bot

### View Real-time Logs
```bash
aws logs tail /aws/lambda/hourstats --follow
```

### Check Function Metrics
```bash
aws cloudwatch get-metric-statistics \
  --namespace AWS/Lambda \
  --metric-name Invocations \
  --dimensions Name=FunctionName,Value=hourstats \
  --start-time $(date -u -d '1 hour ago' +%Y-%m-%dT%H:%M:%S) \
  --end-time $(date -u +%Y-%m-%dT%H:%M:%S) \
  --period 300 \
  --statistics Sum
```

## Troubleshooting

### Common Issues

#### 1. Authentication Error
```bash
# Check your credentials
aws ssm get-parameter --name "/hourstats/bluesky/handle"
```

#### 2. Function Not Triggering
```bash
# Check EventBridge rule
aws events describe-rule --name hourstats-schedule
```

#### 3. Function Timeout
```bash
# Check function configuration
aws lambda get-function-configuration --function-name hourstats
```

### Debug Commands
```bash
# View recent logs
aws logs describe-log-streams --log-group-name /aws/lambda/hourstats --order-by LastEventTime --descending --max-items 1

# Check all SSM parameters
aws ssm get-parameters-by-path --path "/hourstats" --recursive

# Test function manually
aws lambda invoke --function-name hourstats --payload '{"source":"test","time":"2024-01-01T00:00:00Z"}' response.json && cat response.json
```

## Cost

- **Estimated Monthly Cost**: ~$0.65
- **Lambda**: ~$0.50 (2 executions/hour)
- **CloudWatch Logs**: ~$0.10 (14-day retention)
- **SSM Parameters**: ~$0.05 (5 parameters)
- **EventBridge**: $0.00 (first 1M events free)

## Next Steps

1. **Monitor**: Set up CloudWatch alarms for errors
2. **Scale**: Adjust memory/timeout if needed
3. **Customize**: Modify analysis interval or post count
4. **Backup**: Set up parameter backup strategy

## Support

- **Documentation**: See `README_LAMBDA.md` for detailed information
- **Issues**: Check CloudWatch logs for error details
- **Updates**: Use `make -f Makefile.lambda update-lambda` to update code

Your Bluesky HourStats bot is now running on AWS Lambda! 🎉
