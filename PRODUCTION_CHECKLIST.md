# Production Deployment Checklist

Use this checklist to ensure a successful production deployment of the Bluesky HourStats Lambda bot.

## Pre-Deployment Checklist

### ✅ AWS Account Setup
- [ ] AWS account created and verified
- [ ] AWS CLI installed and configured
- [ ] IAM user with appropriate permissions created
- [ ] AWS region selected (us-east-1 recommended)

### ✅ Required Tools
- [ ] AWS CLI installed (`aws --version`)
- [ ] Terraform installed (`terraform --version`)
- [ ] Go installed (`go version`)
- [ ] Git installed (`git --version`)

### ✅ Bluesky Account
- [ ] Bluesky account created
- [ ] App password generated
- [ ] Handle confirmed (e.g., hourstats.bsky.social)

### ✅ Code Preparation
- [ ] Repository cloned
- [ ] Dependencies installed (`go mod tidy`)
- [ ] Code reviewed and tested locally

## Deployment Checklist

### ✅ Configuration
- [ ] SSM parameters configured
  - [ ] `/hourstats/bluesky/handle` set
  - [ ] `/hourstats/bluesky/password` set (SecureString)
  - [ ] `/hourstats/settings/analysis_interval_minutes` set
  - [ ] `/hourstats/settings/top_posts_count` set
  - [ ] `/hourstats/settings/min_engagement_score` set
  - [ ] `/hourstats/settings/dry_run` set to `true`

### ✅ Infrastructure Deployment
- [ ] Lambda function built (`make -f Makefile.lambda build-lambda`)
- [ ] Terraform initialized (`terraform init`)
- [ ] Deployment plan reviewed (`terraform plan`)
- [ ] Infrastructure deployed (`terraform apply`)

### ✅ Function Testing
- [ ] Lambda function invoked successfully
- [ ] Function logs reviewed
- [ ] No errors in CloudWatch logs
- [ ] Function configuration verified

### ✅ EventBridge Setup
- [ ] EventBridge rule created
- [ ] Rule schedule verified (30 minutes)
- [ ] Lambda function target configured
- [ ] Rule enabled and active

## Post-Deployment Checklist

### ✅ Initial Testing
- [ ] Function executes on schedule
- [ ] Dry-run mode working (no actual posts)
- [ ] Logs show successful execution
- [ ] No authentication errors
- [ ] No timeout errors

### ✅ Production Mode
- [ ] Dry-run mode disabled
- [ ] Function posts to Bluesky successfully
- [ ] Posts appear on Bluesky account
- [ ] Post format correct
- [ ] Sentiment indicators working

### ✅ Monitoring Setup
- [ ] CloudWatch dashboard created
- [ ] Error rate alarm configured
- [ ] Duration alarm configured
- [ ] Log retention set to 14 days
- [ ] Monitoring alerts working

### ✅ Security Verification
- [ ] SSM parameters encrypted
- [ ] IAM permissions minimal
- [ ] No hardcoded credentials
- [ ] Function logs don't contain sensitive data

## Ongoing Maintenance Checklist

### ✅ Daily Monitoring
- [ ] Check CloudWatch logs for errors
- [ ] Verify function execution frequency
- [ ] Monitor function performance metrics
- [ ] Check Bluesky posts are appearing

### ✅ Weekly Monitoring
- [ ] Review error rates and patterns
- [ ] Check function duration trends
- [ ] Verify SSM parameter values
- [ ] Review cost usage

### ✅ Monthly Monitoring
- [ ] Update dependencies if needed
- [ ] Review and rotate credentials
- [ ] Check for AWS service updates
- [ ] Review cost optimization opportunities

## Troubleshooting Checklist

### ✅ Common Issues
- [ ] Authentication errors resolved
- [ ] Function timeout issues addressed
- [ ] Memory issues resolved
- [ ] EventBridge trigger issues fixed
- [ ] SSM parameter access issues resolved

### ✅ Debug Steps
- [ ] CloudWatch logs reviewed
- [ ] Function configuration checked
- [ ] SSM parameters verified
- [ ] EventBridge rule status checked
- [ ] IAM permissions verified

## Rollback Checklist

### ✅ If Deployment Fails
- [ ] Terraform state reviewed
- [ ] Failed resources identified
- [ ] Rollback plan created
- [ ] Original server maintained as backup
- [ ] Issues documented for future reference

### ✅ If Function Malfunctions
- [ ] Dry-run mode re-enabled
- [ ] Function logs analyzed
- [ ] Configuration parameters checked
- [ ] EventBridge rule disabled if needed
- [ ] Support team notified if required

## Success Criteria

### ✅ Deployment Successful When:
- [ ] Function executes every 30 minutes
- [ ] Posts appear on Bluesky account
- [ ] No errors in CloudWatch logs
- [ ] Cost within expected range (~$0.65/month)
- [ ] Monitoring alerts configured
- [ ] Documentation updated

### ✅ Production Ready When:
- [ ] All checklists completed
- [ ] Monitoring dashboard active
- [ ] Error handling working
- [ ] Performance within expected range
- [ ] Security requirements met
- [ ] Team trained on monitoring

## Emergency Procedures

### ✅ If Bot Stops Working
1. [ ] Check CloudWatch logs immediately
2. [ ] Verify EventBridge rule status
3. [ ] Check Lambda function status
4. [ ] Verify SSM parameters
5. [ ] Test function manually
6. [ ] Contact support if needed

### ✅ If Bot Posts Incorrectly
1. [ ] Enable dry-run mode immediately
2. [ ] Review recent logs
3. [ ] Check configuration parameters
4. [ ] Test with dry-run mode
5. [ ] Fix configuration issues
6. [ ] Re-enable production mode

## Contact Information

### ✅ Support Resources
- [ ] AWS Support plan activated
- [ ] CloudWatch documentation reviewed
- [ ] Lambda troubleshooting guide available
- [ ] Team contact information updated
- [ ] Emergency escalation procedures defined

---

**Remember**: Always test in dry-run mode first, monitor closely after deployment, and have a rollback plan ready!
