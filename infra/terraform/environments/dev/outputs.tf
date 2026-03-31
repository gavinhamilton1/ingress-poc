################################################################################
# VPC
################################################################################

output "vpc_id" {
  description = "ID of the VPC"
  value       = module.vpc.vpc_id
}

output "private_subnet_ids" {
  description = "IDs of the private subnets"
  value       = module.vpc.private_subnet_ids
}

output "public_subnet_ids" {
  description = "IDs of the public subnets"
  value       = module.vpc.public_subnet_ids
}

################################################################################
# EKS Control Plane
################################################################################

output "cp_cluster_name" {
  description = "Name of the control plane EKS cluster"
  value       = module.eks_cp.cluster_name
}

output "cp_cluster_endpoint" {
  description = "Endpoint for the control plane cluster API"
  value       = module.eks_cp.cluster_endpoint
}

output "cp_cluster_certificate_authority" {
  description = "Base64 encoded CA cert for the CP cluster"
  value       = module.eks_cp.cluster_certificate_authority_data
  sensitive   = true
}

output "cp_oidc_provider_arn" {
  description = "ARN of the CP cluster OIDC provider"
  value       = module.eks_cp.oidc_provider_arn
}

################################################################################
# EKS Data Plane Clusters
################################################################################

output "dp_clusters" {
  description = "Map of data plane cluster outputs"
  value = {
    for k, v in module.eks_dp : k => {
      cluster_name     = v.cluster_name
      cluster_endpoint = v.cluster_endpoint
      cluster_arn      = v.cluster_arn
      oidc_provider_arn = v.oidc_provider_arn
    }
  }
}

output "dp_cluster_endpoints" {
  description = "Endpoints for all data plane clusters"
  value       = { for k, v in module.eks_dp : k => v.cluster_endpoint }
}

################################################################################
# RDS
################################################################################

output "rds_endpoint" {
  description = "Connection endpoint for the RDS instance"
  value       = module.rds.endpoint
}

output "rds_address" {
  description = "Hostname of the RDS instance"
  value       = module.rds.address
}

output "rds_port" {
  description = "Port of the RDS instance"
  value       = module.rds.port
}

output "rds_db_name" {
  description = "Name of the database"
  value       = module.rds.db_name
}

output "rds_master_user_secret_arn" {
  description = "ARN of the Secrets Manager secret containing the DB master password"
  value       = module.rds.master_user_secret_arn
}

################################################################################
# IRSA
################################################################################

output "management_api_role_arn" {
  description = "IAM role ARN for the management-api service account (DP cluster access)"
  value       = module.irsa_management_api.role_arn
}

output "management_api_rds_role_arn" {
  description = "IAM role ARN for the management-api-rds service account (RDS secrets access)"
  value       = module.irsa_management_api_rds.role_arn
}

################################################################################
# Kubeconfig helpers
################################################################################

output "cp_kubeconfig_command" {
  description = "AWS CLI command to configure kubeconfig for the CP cluster"
  value       = "aws eks update-kubeconfig --name ${module.eks_cp.cluster_name} --region ${data.aws_region.current.name}"
}

output "dp_kubeconfig_commands" {
  description = "AWS CLI commands to configure kubeconfig for each DP cluster"
  value = {
    for k, v in module.eks_dp : k =>
    "aws eks update-kubeconfig --name ${v.cluster_name} --region ${data.aws_region.current.name}"
  }
}
