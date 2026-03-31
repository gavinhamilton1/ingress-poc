################################################################################
# Data Sources
################################################################################

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

################################################################################
# Locals
################################################################################

locals {
  common_tags = merge(var.tags, {
    "terraform/module"               = "eks"
    "kubernetes.io/cluster/${var.cluster_name}" = "owned"
  })
}

################################################################################
# EKS Cluster
################################################################################

resource "aws_eks_cluster" "this" {
  name     = var.cluster_name
  version  = var.cluster_version
  role_arn = aws_iam_role.cluster.arn

  vpc_config {
    subnet_ids              = var.subnet_ids
    endpoint_public_access  = var.cluster_endpoint_public_access
    endpoint_private_access = var.cluster_endpoint_private_access
    public_access_cidrs     = var.cluster_endpoint_public_access_cidrs
    security_group_ids      = [aws_security_group.cluster.id]
  }

  enabled_cluster_log_types = var.cluster_log_types

  tags = merge(local.common_tags, {
    Name = var.cluster_name
  })

  depends_on = [
    aws_iam_role_policy_attachment.cluster_policies,
    aws_cloudwatch_log_group.cluster,
  ]
}

################################################################################
# CloudWatch Log Group
################################################################################

resource "aws_cloudwatch_log_group" "cluster" {
  name              = "/aws/eks/${var.cluster_name}/cluster"
  retention_in_days = var.cluster_log_retention_days

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-logs"
  })
}

################################################################################
# Cluster Security Group
################################################################################

resource "aws_security_group" "cluster" {
  name_prefix = "${var.cluster_name}-cluster-"
  description = "Security group for EKS cluster ${var.cluster_name} control plane"
  vpc_id      = var.vpc_id

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-cluster-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "cluster_egress" {
  description       = "Allow all egress from cluster"
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.cluster.id
}

################################################################################
# Cluster Addons
################################################################################

resource "aws_eks_addon" "this" {
  for_each = var.cluster_addons

  cluster_name                = aws_eks_cluster.this.name
  addon_name                  = each.key
  addon_version               = each.value.version
  resolve_conflicts_on_create = "OVERWRITE"
  resolve_conflicts_on_update = "OVERWRITE"

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-addon-${each.key}"
  })

  depends_on = [
    aws_eks_node_group.this,
  ]
}

################################################################################
# Managed Node Groups
################################################################################

resource "aws_eks_node_group" "this" {
  for_each = var.node_groups

  cluster_name    = aws_eks_cluster.this.name
  node_group_name = each.key
  node_role_arn   = aws_iam_role.node.arn
  subnet_ids      = var.subnet_ids

  instance_types = each.value.instance_types
  capacity_type  = each.value.capacity_type
  disk_size      = each.value.disk_size

  scaling_config {
    min_size     = each.value.min_size
    max_size     = each.value.max_size
    desired_size = each.value.desired_size
  }

  update_config {
    max_unavailable = 1
  }

  labels = each.value.labels

  dynamic "taint" {
    for_each = each.value.taints
    content {
      key    = taint.value.key
      value  = taint.value.value
      effect = taint.value.effect
    }
  }

  tags = merge(local.common_tags, {
    Name = "${var.cluster_name}-${each.key}"
  })

  depends_on = [
    aws_iam_role_policy_attachment.node_policies,
  ]

  lifecycle {
    ignore_changes = [scaling_config[0].desired_size]
  }
}

################################################################################
# aws-auth ConfigMap
# Note: For EKS 1.24+, consider using aws-auth via the Access Entries API
################################################################################

locals {
  # Node role mapping is always included
  node_role_mapping = [{
    rolearn  = aws_iam_role.node.arn
    username = "system:node:{{EC2PrivateDNSName}}"
    groups   = ["system:bootstrappers", "system:nodes"]
  }]

  aws_auth_configmap_data = {
    mapRoles = yamlencode(concat(local.node_role_mapping, var.aws_auth_roles))
    mapUsers = yamlencode(var.aws_auth_users)
  }
}
