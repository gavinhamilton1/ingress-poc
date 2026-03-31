################################################################################
# Data Sources
################################################################################

data "aws_region" "current" {}

data "aws_availability_zones" "available" {
  state = "available"

  filter {
    name   = "opt-in-status"
    values = ["opt-in-not-required"]
  }
}

################################################################################
# Locals
################################################################################

locals {
  azs = length(var.azs) > 0 ? var.azs : slice(data.aws_availability_zones.available.names, 0, 3)

  # Auto-calculate subnet CIDRs if not provided
  # Public subnets:  x.x.0.0/20, x.x.16.0/20, x.x.32.0/20
  # Private subnets: x.x.128.0/20, x.x.144.0/20, x.x.160.0/20
  public_subnet_cidrs = length(var.public_subnet_cidrs) > 0 ? var.public_subnet_cidrs : [
    for i in range(length(local.azs)) : cidrsubnet(var.cidr_block, 4, i)
  ]

  private_subnet_cidrs = length(var.private_subnet_cidrs) > 0 ? var.private_subnet_cidrs : [
    for i in range(length(local.azs)) : cidrsubnet(var.cidr_block, 4, i + 8)
  ]

  nat_gateway_count = var.single_nat_gateway ? 1 : length(local.azs)

  # EKS subnet tags for auto-discovery
  eks_public_subnet_tags = merge(
    { "kubernetes.io/role/elb" = "1" },
    { for name in var.eks_cluster_names : "kubernetes.io/cluster/${name}" => "shared" }
  )

  eks_private_subnet_tags = merge(
    { "kubernetes.io/role/internal-elb" = "1" },
    { for name in var.eks_cluster_names : "kubernetes.io/cluster/${name}" => "shared" }
  )

  common_tags = merge(var.tags, {
    "terraform/module" = "vpc"
  })
}

################################################################################
# VPC
################################################################################

resource "aws_vpc" "this" {
  cidr_block           = var.cidr_block
  enable_dns_hostnames = var.enable_dns_hostnames
  enable_dns_support   = var.enable_dns_support

  tags = merge(local.common_tags, {
    Name = var.name
  })
}

################################################################################
# Internet Gateway
################################################################################

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id

  tags = merge(local.common_tags, {
    Name = "${var.name}-igw"
  })
}

################################################################################
# Public Subnets
################################################################################

resource "aws_subnet" "public" {
  count = length(local.azs)

  vpc_id                  = aws_vpc.this.id
  cidr_block              = local.public_subnet_cidrs[count.index]
  availability_zone       = local.azs[count.index]
  map_public_ip_on_launch = true

  tags = merge(local.common_tags, local.eks_public_subnet_tags, {
    Name = "${var.name}-public-${local.azs[count.index]}"
    Tier = "public"
  })
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  tags = merge(local.common_tags, {
    Name = "${var.name}-public"
  })
}

resource "aws_route" "public_internet" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.this.id
}

resource "aws_route_table_association" "public" {
  count = length(local.azs)

  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

################################################################################
# Private Subnets
################################################################################

resource "aws_subnet" "private" {
  count = length(local.azs)

  vpc_id            = aws_vpc.this.id
  cidr_block        = local.private_subnet_cidrs[count.index]
  availability_zone = local.azs[count.index]

  tags = merge(local.common_tags, local.eks_private_subnet_tags, {
    Name = "${var.name}-private-${local.azs[count.index]}"
    Tier = "private"
  })
}

resource "aws_route_table" "private" {
  count = local.nat_gateway_count

  vpc_id = aws_vpc.this.id

  tags = merge(local.common_tags, {
    Name = var.single_nat_gateway ? "${var.name}-private" : "${var.name}-private-${local.azs[count.index]}"
  })
}

resource "aws_route" "private_nat" {
  count = local.nat_gateway_count

  route_table_id         = aws_route_table.private[count.index].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.this[count.index].id
}

resource "aws_route_table_association" "private" {
  count = length(local.azs)

  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[var.single_nat_gateway ? 0 : count.index].id
}

################################################################################
# NAT Gateway(s)
################################################################################

resource "aws_eip" "nat" {
  count = local.nat_gateway_count

  domain = "vpc"

  tags = merge(local.common_tags, {
    Name = var.single_nat_gateway ? "${var.name}-nat" : "${var.name}-nat-${local.azs[count.index]}"
  })

  depends_on = [aws_internet_gateway.this]
}

resource "aws_nat_gateway" "this" {
  count = local.nat_gateway_count

  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = merge(local.common_tags, {
    Name = var.single_nat_gateway ? "${var.name}-nat" : "${var.name}-nat-${local.azs[count.index]}"
  })

  depends_on = [aws_internet_gateway.this]
}
