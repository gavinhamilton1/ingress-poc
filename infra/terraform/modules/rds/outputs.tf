output "instance_id" {
  description = "ID of the RDS instance"
  value       = aws_db_instance.this.id
}

output "instance_arn" {
  description = "ARN of the RDS instance"
  value       = aws_db_instance.this.arn
}

output "endpoint" {
  description = "Connection endpoint for the RDS instance"
  value       = aws_db_instance.this.endpoint
}

output "address" {
  description = "Hostname of the RDS instance (without port)"
  value       = aws_db_instance.this.address
}

output "port" {
  description = "Port of the RDS instance"
  value       = aws_db_instance.this.port
}

output "db_name" {
  description = "Name of the initial database"
  value       = aws_db_instance.this.db_name
}

output "username" {
  description = "Master username"
  value       = aws_db_instance.this.username
}

output "master_user_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the master password"
  value       = try(aws_db_instance.this.master_user_secret[0].secret_arn, null)
}

output "security_group_id" {
  description = "ID of the database security group"
  value       = aws_security_group.this.id
}
