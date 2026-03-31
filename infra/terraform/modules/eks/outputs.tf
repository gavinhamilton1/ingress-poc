output "cluster_id" {
  description = "ID of the EKS cluster"
  value       = aws_eks_cluster.this.id
}

output "cluster_name" {
  description = "Name of the EKS cluster"
  value       = aws_eks_cluster.this.name
}

output "cluster_arn" {
  description = "ARN of the EKS cluster"
  value       = aws_eks_cluster.this.arn
}

output "cluster_endpoint" {
  description = "Endpoint URL for the EKS cluster API server"
  value       = aws_eks_cluster.this.endpoint
}

output "cluster_certificate_authority_data" {
  description = "Base64 encoded certificate data for the cluster"
  value       = aws_eks_cluster.this.certificate_authority[0].data
}

output "cluster_version" {
  description = "Kubernetes version of the cluster"
  value       = aws_eks_cluster.this.version
}

output "cluster_security_group_id" {
  description = "ID of the cluster security group"
  value       = aws_security_group.cluster.id
}

output "cluster_primary_security_group_id" {
  description = "ID of the EKS-managed cluster security group"
  value       = aws_eks_cluster.this.vpc_config[0].cluster_security_group_id
}

output "oidc_provider_arn" {
  description = "ARN of the OIDC provider for IRSA"
  value       = aws_iam_openid_connect_provider.cluster.arn
}

output "oidc_provider_url" {
  description = "URL of the OIDC provider (without https://)"
  value       = replace(aws_eks_cluster.this.identity[0].oidc[0].issuer, "https://", "")
}

output "oidc_issuer" {
  description = "Full OIDC issuer URL"
  value       = aws_eks_cluster.this.identity[0].oidc[0].issuer
}

output "cluster_role_arn" {
  description = "ARN of the cluster IAM role"
  value       = aws_iam_role.cluster.arn
}

output "node_role_arn" {
  description = "ARN of the node group IAM role"
  value       = aws_iam_role.node.arn
}

output "node_security_group_id" {
  description = "ID of the EKS-managed node security group (from cluster primary SG)"
  value       = aws_eks_cluster.this.vpc_config[0].cluster_security_group_id
}

output "aws_auth_configmap_data" {
  description = "aws-auth ConfigMap data for use with kubernetes_config_map_v1_data"
  value       = local.aws_auth_configmap_data
}
