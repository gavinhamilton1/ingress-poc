################################################################################
# Data Sources
################################################################################

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

################################################################################
# Locals
################################################################################

locals {
  all_cluster_names = concat(
    [var.cp_cluster_name],
    [for k, v in var.dp_clusters : v.cluster_name]
  )

  common_tags = {
    Project     = var.project
    Environment = var.environment
    ManagedBy   = "terraform"
  }
}

################################################################################
# VPC
################################################################################

module "vpc" {
  source = "../../modules/vpc"

  name               = "${var.project}-${var.environment}"
  cidr_block         = var.vpc_cidr
  single_nat_gateway = var.single_nat_gateway
  eks_cluster_names  = local.all_cluster_names
  tags               = local.common_tags
}

################################################################################
# EKS Control Plane Cluster
################################################################################

module "eks_cp" {
  source = "../../modules/eks"

  cluster_name    = var.cp_cluster_name
  cluster_version = var.cp_cluster_version
  vpc_id          = module.vpc.vpc_id
  subnet_ids      = module.vpc.private_subnet_ids

  node_groups = {
    cp-default = {
      instance_types = var.cp_node_instance_types
      min_size       = var.cp_node_min_size
      max_size       = var.cp_node_max_size
      desired_size   = var.cp_node_desired_size
      labels = {
        "ingress.io/role" = "controlplane"
      }
    }
  }

  cluster_endpoint_public_access  = true
  cluster_endpoint_private_access = true

  tags = local.common_tags
}

################################################################################
# EKS Data Plane Clusters (N-region parameterizable)
################################################################################

module "eks_dp" {
  source   = "../../modules/eks"
  for_each = var.dp_clusters

  cluster_name    = each.value.cluster_name
  cluster_version = each.value.cluster_version
  vpc_id          = module.vpc.vpc_id
  subnet_ids      = module.vpc.private_subnet_ids

  node_groups = {
    dp-default = {
      instance_types = each.value.instance_types
      min_size       = each.value.min_size
      max_size       = each.value.max_size
      desired_size   = each.value.desired_size
      labels         = each.value.node_labels
    }
  }

  cluster_endpoint_public_access  = true
  cluster_endpoint_private_access = true

  tags = merge(local.common_tags, {
    "ingress.io/cluster-type" = "dataplane"
    "ingress.io/region-key"   = each.key
  })
}

################################################################################
# RDS (PostgreSQL)
################################################################################

module "rds" {
  source = "../../modules/rds"

  identifier    = "${var.project}-${var.environment}"
  engine_version = var.rds_engine_version
  instance_class = var.rds_instance_class
  multi_az       = var.rds_multi_az

  allocated_storage = var.rds_allocated_storage
  db_name           = var.rds_db_name

  vpc_id     = module.vpc.vpc_id
  subnet_ids = module.vpc.private_subnet_ids

  # Allow access from both CP and all DP cluster node security groups
  allowed_security_group_ids = concat(
    [module.eks_cp.cluster_primary_security_group_id],
    [for k, v in module.eks_dp : v.cluster_primary_security_group_id]
  )

  deletion_protection = false
  skip_final_snapshot = true

  tags = local.common_tags
}

################################################################################
# IAM Policy: Data Plane Cluster API Access
#
# Grants the management-api running on the CP cluster the ability to interact
# with EKS DP clusters (describe, list nodegroups, update, etc.)
################################################################################

data "aws_iam_policy_document" "dp_cluster_access" {
  statement {
    sid    = "DescribeAndListDPClusters"
    effect = "Allow"
    actions = [
      "eks:DescribeCluster",
      "eks:ListClusters",
      "eks:ListNodegroups",
      "eks:DescribeNodegroup",
      "eks:ListUpdates",
      "eks:DescribeUpdate",
      "eks:AccessKubernetesApi",
    ]
    resources = concat(
      [for k, v in module.eks_dp : v.cluster_arn],
      [for k, v in module.eks_dp : "${v.cluster_arn}/*"]
    )
  }

  statement {
    sid    = "ListAllClusters"
    effect = "Allow"
    actions = [
      "eks:ListClusters",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "STSGetCallerIdentity"
    effect = "Allow"
    actions = [
      "sts:GetCallerIdentity",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "dp_cluster_access" {
  name        = "${var.project}-${var.environment}-dp-cluster-access"
  description = "Allows management-api to access data plane EKS clusters"
  policy      = data.aws_iam_policy_document.dp_cluster_access.json

  tags = local.common_tags
}

################################################################################
# IRSA: management-api on CP cluster -> DP cluster access
################################################################################

module "irsa_management_api" {
  source = "../../modules/irsa"

  role_name            = "${var.project}-${var.environment}-management-api"
  oidc_provider_arn    = module.eks_cp.oidc_provider_arn
  oidc_provider_url    = module.eks_cp.oidc_provider_url
  namespace            = "ingress-dp"
  service_account_name = "management-api"

  policy_arns = [
    aws_iam_policy.dp_cluster_access.arn,
  ]

  tags = local.common_tags
}

################################################################################
# IRSA: management-api on CP cluster -> RDS/Secrets Manager access
################################################################################

data "aws_iam_policy_document" "rds_secrets_access" {
  statement {
    sid    = "SecretsManagerReadDB"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
    ]
    resources = compact([
      module.rds.master_user_secret_arn != null ? module.rds.master_user_secret_arn : "",
      module.rds.master_user_secret_arn != null ? "${module.rds.master_user_secret_arn}*" : "",
    ])
  }
}

resource "aws_iam_policy" "rds_secrets_access" {
  name        = "${var.project}-${var.environment}-rds-secrets-access"
  description = "Allows reading RDS master user secret from Secrets Manager"
  policy      = data.aws_iam_policy_document.rds_secrets_access.json

  tags = local.common_tags
}

module "irsa_management_api_rds" {
  source = "../../modules/irsa"

  role_name            = "${var.project}-${var.environment}-management-api-rds"
  oidc_provider_arn    = module.eks_cp.oidc_provider_arn
  oidc_provider_url    = module.eks_cp.oidc_provider_url
  namespace            = "ingress-dp"
  service_account_name = "management-api-rds"

  policy_arns = [
    aws_iam_policy.rds_secrets_access.arn,
  ]

  tags = local.common_tags
}
