################################################################################
# Locals
################################################################################

locals {
  common_tags = merge(var.tags, {
    "terraform/module" = "rds"
  })
}

################################################################################
# DB Subnet Group
################################################################################

resource "aws_db_subnet_group" "this" {
  name       = "${var.identifier}-subnet-group"
  subnet_ids = var.subnet_ids

  tags = merge(local.common_tags, {
    Name = "${var.identifier}-subnet-group"
  })
}

################################################################################
# Parameter Group
################################################################################

resource "aws_db_parameter_group" "this" {
  name_prefix = "${var.identifier}-pg16-"
  family      = "postgres16"
  description = "Parameter group for ${var.identifier}"

  parameter {
    name  = "log_connections"
    value = "1"
  }

  parameter {
    name  = "log_disconnections"
    value = "1"
  }

  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }

  dynamic "parameter" {
    for_each = var.parameter_group_parameters
    content {
      name         = parameter.value.name
      value        = parameter.value.value
      apply_method = parameter.value.apply_method
    }
  }

  tags = merge(local.common_tags, {
    Name = "${var.identifier}-pg16"
  })

  lifecycle {
    create_before_destroy = true
  }
}

################################################################################
# Security Group
################################################################################

resource "aws_security_group" "this" {
  name_prefix = "${var.identifier}-db-"
  description = "Security group for RDS instance ${var.identifier}"
  vpc_id      = var.vpc_id

  tags = merge(local.common_tags, {
    Name = "${var.identifier}-db-sg"
  })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group_rule" "ingress_sg" {
  count = length(var.allowed_security_group_ids)

  description              = "Allow PostgreSQL access from security group"
  type                     = "ingress"
  from_port                = var.port
  to_port                  = var.port
  protocol                 = "tcp"
  source_security_group_id = var.allowed_security_group_ids[count.index]
  security_group_id        = aws_security_group.this.id
}

resource "aws_security_group_rule" "ingress_cidr" {
  count = length(var.allowed_cidr_blocks) > 0 ? 1 : 0

  description       = "Allow PostgreSQL access from CIDR blocks"
  type              = "ingress"
  from_port         = var.port
  to_port           = var.port
  protocol          = "tcp"
  cidr_blocks       = var.allowed_cidr_blocks
  security_group_id = aws_security_group.this.id
}

resource "aws_security_group_rule" "egress" {
  description       = "Allow all egress"
  type              = "egress"
  from_port         = 0
  to_port           = 0
  protocol          = "-1"
  cidr_blocks       = ["0.0.0.0/0"]
  security_group_id = aws_security_group.this.id
}

################################################################################
# Enhanced Monitoring IAM Role
################################################################################

resource "aws_iam_role" "monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0

  name_prefix        = "${var.identifier}-rds-mon-"
  assume_role_policy = data.aws_iam_policy_document.monitoring_assume[0].json

  tags = merge(local.common_tags, {
    Name = "${var.identifier}-rds-monitoring"
  })
}

data "aws_iam_policy_document" "monitoring_assume" {
  count = var.monitoring_interval > 0 ? 1 : 0

  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]

    principals {
      type        = "Service"
      identifiers = ["monitoring.rds.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy_attachment" "monitoring" {
  count = var.monitoring_interval > 0 ? 1 : 0

  role       = aws_iam_role.monitoring[0].name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonRDSEnhancedMonitoringRole"
}

################################################################################
# RDS Instance
################################################################################

resource "aws_db_instance" "this" {
  identifier = var.identifier

  engine         = "postgres"
  engine_version = var.engine_version
  instance_class = var.instance_class

  allocated_storage     = var.allocated_storage
  max_allocated_storage = var.max_allocated_storage > 0 ? var.max_allocated_storage : null
  storage_type          = "gp3"
  storage_encrypted     = var.storage_encrypted

  db_name  = var.db_name
  username = var.username
  port     = var.port

  manage_master_user_password = var.manage_master_user_password

  multi_az = var.multi_az

  db_subnet_group_name   = aws_db_subnet_group.this.name
  vpc_security_group_ids = [aws_security_group.this.id]
  parameter_group_name   = aws_db_parameter_group.this.name

  backup_retention_period = var.backup_retention_period
  backup_window           = var.backup_window
  maintenance_window      = var.maintenance_window

  deletion_protection = var.deletion_protection
  skip_final_snapshot = var.skip_final_snapshot
  final_snapshot_identifier = var.skip_final_snapshot ? null : "${var.identifier}-final-snapshot"

  performance_insights_enabled = var.performance_insights_enabled
  monitoring_interval          = var.monitoring_interval
  monitoring_role_arn          = var.monitoring_interval > 0 ? aws_iam_role.monitoring[0].arn : null

  auto_minor_version_upgrade = true
  copy_tags_to_snapshot      = true

  tags = merge(local.common_tags, {
    Name = var.identifier
  })
}
