#!/bin/bash

# Production Deployment Script for Bluesky HourStats Lambda
# This script automates the deployment process

set -e  # Exit on any error

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
FUNCTION_NAME="hourstats"
AWS_REGION="us-east-1"
TERRAFORM_DIR="terraform"
LAMBDA_DIR="cmd/lambda"

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to check prerequisites
check_prerequisites() {
    print_status "Checking prerequisites..."
    
    if ! command_exists aws; then
        print_error "AWS CLI is not installed. Please install it first."
        exit 1
    fi
    
    if ! command_exists terraform; then
        print_error "Terraform is not installed. Please install it first."
        exit 1
    fi
    
    if ! command_exists go; then
        print_error "Go is not installed. Please install it first."
        exit 1
    fi
    
    # Check AWS credentials
    if ! aws sts get-caller-identity >/dev/null 2>&1; then
        print_error "AWS credentials not configured. Please run 'aws configure' first."
        exit 1
    fi
    
    print_success "All prerequisites met"
}

# Function to get user input
get_user_input() {
    print_status "Please provide the following information:"
    
    read -p "Bluesky handle (e.g., hourstats.bsky.social): " BLUESKY_HANDLE
    read -s -p "Bluesky app password: " BLUESKY_PASSWORD
    echo
    
    read -p "Analysis interval in minutes (default: 30): " ANALYSIS_INTERVAL
    ANALYSIS_INTERVAL=${ANALYSIS_INTERVAL:-30}
    
    read -p "Number of top posts (default: 5): " TOP_POSTS_COUNT
    TOP_POSTS_COUNT=${TOP_POSTS_COUNT:-5}
    
    read -p "Minimum engagement score (default: 10): " MIN_ENGAGEMENT
    MIN_ENGAGEMENT=${MIN_ENGAGEMENT:-10}
    
    read -p "Start in dry-run mode? (y/n, default: y): " DRY_RUN
    DRY_RUN=${DRY_RUN:-y}
    
    if [[ $DRY_RUN =~ ^[Yy]$ ]]; then
        DRY_RUN_VALUE="true"
    else
        DRY_RUN_VALUE="false"
    fi
}

# Function to set up SSM parameters
setup_ssm_parameters() {
    print_status "Setting up SSM parameters..."
    
    # Set Bluesky credentials
    aws ssm put-parameter \
        --name "/hourstats/bluesky/handle" \
        --value "$BLUESKY_HANDLE" \
        --type "String" \
        --description "Bluesky handle for the bot" \
        --overwrite >/dev/null
    
    aws ssm put-parameter \
        --name "/hourstats/bluesky/password" \
        --value "$BLUESKY_PASSWORD" \
        --type "SecureString" \
        --description "Bluesky app password" \
        --overwrite >/dev/null
    
    # Set configuration parameters
    aws ssm put-parameter \
        --name "/hourstats/settings/analysis_interval_minutes" \
        --value "$ANALYSIS_INTERVAL" \
        --type "String" \
        --description "Analysis interval in minutes" \
        --overwrite >/dev/null
    
    aws ssm put-parameter \
        --name "/hourstats/settings/top_posts_count" \
        --value "$TOP_POSTS_COUNT" \
        --type "String" \
        --description "Number of top posts to include" \
        --overwrite >/dev/null
    
    aws ssm put-parameter \
        --name "/hourstats/settings/min_engagement_score" \
        --value "$MIN_ENGAGEMENT" \
        --type "String" \
        --description "Minimum engagement score" \
        --overwrite >/dev/null
    
    aws ssm put-parameter \
        --name "/hourstats/settings/dry_run" \
        --value "$DRY_RUN_VALUE" \
        --type "String" \
        --description "Enable dry run mode" \
        --overwrite >/dev/null
    
    print_success "SSM parameters configured"
}

# Function to build Lambda function
build_lambda() {
    print_status "Building Lambda function..."
    
    cd "$LAMBDA_DIR"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o main .
    zip hourstats.zip main
    mv hourstats.zip "../$TERRAFORM_DIR/"
    rm -f main
    cd ..
    
    print_success "Lambda function built and packaged"
}

# Function to deploy infrastructure
deploy_infrastructure() {
    print_status "Deploying infrastructure with Terraform..."
    
    cd "$TERRAFORM_DIR"
    
    # Initialize Terraform
    terraform init
    
    # Plan deployment
    print_status "Planning deployment..."
    terraform plan
    
    # Ask for confirmation
    read -p "Do you want to proceed with the deployment? (y/n): " CONFIRM
    if [[ ! $CONFIRM =~ ^[Yy]$ ]]; then
        print_warning "Deployment cancelled by user"
        exit 0
    fi
    
    # Apply deployment
    print_status "Applying deployment..."
    terraform apply -auto-approve
    
    cd ..
    
    print_success "Infrastructure deployed successfully"
}

# Function to test deployment
test_deployment() {
    print_status "Testing deployment..."
    
    # Wait a moment for the function to be ready
    sleep 10
    
    # Test the function
    print_status "Invoking Lambda function..."
    aws lambda invoke \
        --function-name "$FUNCTION_NAME" \
        --region "$AWS_REGION" \
        --payload '{"source":"deployment-test","time":"2024-01-01T00:00:00Z"}' \
        response.json
    
    # Check response
    if [ -f response.json ]; then
        print_status "Function response:"
        cat response.json
        echo
        
        # Check if response contains success
        if grep -q "200" response.json; then
            print_success "Lambda function test successful"
        else
            print_warning "Lambda function test completed with warnings"
        fi
        
        rm -f response.json
    else
        print_error "Failed to invoke Lambda function"
        exit 1
    fi
}

# Function to show monitoring information
show_monitoring_info() {
    print_status "Deployment completed! Here's how to monitor your bot:"
    echo
    
    echo "📊 CloudWatch Logs:"
    echo "   aws logs tail /aws/lambda/$FUNCTION_NAME --follow --region $AWS_REGION"
    echo
    
    echo "🔍 Function Status:"
    echo "   aws lambda get-function --function-name $FUNCTION_NAME --region $AWS_REGION"
    echo
    
    echo "📈 EventBridge Rule:"
    echo "   aws events describe-rule --name hourstats-schedule --region $AWS_REGION"
    echo
    
    echo "⚙️  SSM Parameters:"
    echo "   aws ssm get-parameters-by-path --path /hourstats --recursive --region $AWS_REGION"
    echo
    
    if [[ $DRY_RUN_VALUE == "true" ]]; then
        print_warning "The bot is running in DRY RUN mode. To enable posting:"
        echo "   aws ssm put-parameter --name /hourstats/settings/dry_run --value false --type String --overwrite"
        echo
    fi
    
    print_success "Your Bluesky HourStats bot is now running on AWS Lambda!"
    echo "Estimated monthly cost: ~$0.65"
}

# Function to create monitoring dashboard
create_monitoring_dashboard() {
    print_status "Creating CloudWatch dashboard..."
    
    aws cloudwatch put-dashboard \
        --dashboard-name "HourStats" \
        --dashboard-body '{
            "widgets": [
                {
                    "type": "metric",
                    "x": 0,
                    "y": 0,
                    "width": 12,
                    "height": 6,
                    "properties": {
                        "metrics": [
                            ["AWS/Lambda", "Invocations", "FunctionName", "'$FUNCTION_NAME'"],
                            ["AWS/Lambda", "Errors", "FunctionName", "'$FUNCTION_NAME'"],
                            ["AWS/Lambda", "Duration", "FunctionName", "'$FUNCTION_NAME'"]
                        ],
                        "period": 300,
                        "stat": "Sum",
                        "region": "'$AWS_REGION'",
                        "title": "HourStats Lambda Metrics"
                    }
                }
            ]
        }' >/dev/null
    
    print_success "CloudWatch dashboard created"
}

# Main deployment function
main() {
    echo "🚀 Bluesky HourStats Lambda Production Deployment"
    echo "=================================================="
    echo
    
    # Check prerequisites
    check_prerequisites
    
    # Get user input
    get_user_input
    
    # Set up SSM parameters
    setup_ssm_parameters
    
    # Build Lambda function
    build_lambda
    
    # Deploy infrastructure
    deploy_infrastructure
    
    # Test deployment
    test_deployment
    
    # Create monitoring dashboard
    create_monitoring_dashboard
    
    # Show monitoring information
    show_monitoring_info
}

# Run main function
main "$@"
