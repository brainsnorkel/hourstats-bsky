# Terraform Backend Configuration
# This stores the state file remotely in S3 for persistence and team collaboration

terraform {
  backend "s3" {
    bucket         = "hourstats-terraform-state"
    key            = "hourstats/terraform.tfstate"
    region         = "us-east-1"
    encrypt        = true
    dynamodb_table = "hourstats-terraform-locks"
  }
}
