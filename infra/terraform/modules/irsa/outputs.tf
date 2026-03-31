output "role_arn" {
  description = "ARN of the IAM role (use as eks.amazonaws.com/role-arn annotation)"
  value       = aws_iam_role.this.arn
}

output "role_name" {
  description = "Name of the IAM role"
  value       = aws_iam_role.this.name
}

output "role_id" {
  description = "Unique ID of the IAM role"
  value       = aws_iam_role.this.unique_id
}

output "service_account_annotation" {
  description = "Annotation map to add to the Kubernetes ServiceAccount"
  value = {
    "eks.amazonaws.com/role-arn" = aws_iam_role.this.arn
  }
}
