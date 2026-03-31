################################################################################
# Locals
################################################################################

locals {
  common_tags = merge(var.tags, {
    "terraform/module" = "irsa"
  })
}

################################################################################
# IAM Role with OIDC Trust Policy
################################################################################

data "aws_iam_policy_document" "assume_role" {
  statement {
    sid     = "AllowServiceAccountAssumeRole"
    effect  = "Allow"
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${var.oidc_provider_url}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "${var.oidc_provider_url}:sub"
      values   = ["system:serviceaccount:${var.namespace}:${var.service_account_name}"]
    }
  }
}

resource "aws_iam_role" "this" {
  name                 = var.role_name
  assume_role_policy   = data.aws_iam_policy_document.assume_role.json
  max_session_duration = var.max_session_duration

  tags = merge(local.common_tags, {
    Name                               = var.role_name
    "kubernetes/service-account"       = var.service_account_name
    "kubernetes/namespace"             = var.namespace
  })
}

################################################################################
# Managed Policy Attachments
################################################################################

resource "aws_iam_role_policy_attachment" "this" {
  for_each = toset(var.policy_arns)

  role       = aws_iam_role.this.name
  policy_arn = each.value
}

################################################################################
# Inline Policies
################################################################################

resource "aws_iam_role_policy" "inline" {
  for_each = var.inline_policies

  name   = each.key
  role   = aws_iam_role.this.id
  policy = each.value
}
