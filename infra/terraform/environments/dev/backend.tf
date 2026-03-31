################################################################################
# Terraform Backend Configuration
#
# Uncomment the S3 backend block below once you have created the S3 bucket
# and DynamoDB table for state management.
#
# To bootstrap:
#   1. Run `terraform init` with local backend first
#   2. Create the S3 bucket and DynamoDB table (or use a separate bootstrap config)
#   3. Uncomment the backend block
#   4. Run `terraform init -migrate-state` to move state to S3
################################################################################

terraform {
  # backend "s3" {
  #   bucket         = "ingress-poc-terraform-state"
  #   key            = "environments/dev/terraform.tfstate"
  #   region         = "us-east-1"
  #   encrypt        = true
  #   dynamodb_table = "ingress-poc-terraform-locks"
  # }
}
