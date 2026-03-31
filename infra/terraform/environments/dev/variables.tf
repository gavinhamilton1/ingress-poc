################################################################################
# General
################################################################################

variable "aws_region" {
  description = "AWS region for all resources"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (e.g., dev, staging, prod)"
  type        = string
  default     = "dev"
}

variable "project" {
  description = "Project name"
  type        = string
  default     = "ingress-poc"
}

################################################################################
# VPC
################################################################################

variable "vpc_cidr" {
  description = "CIDR block for the VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "single_nat_gateway" {
  description = "Use a single NAT gateway (cost savings for dev)"
  type        = bool
  default     = true
}

################################################################################
# EKS Control Plane Cluster
################################################################################

variable "cp_cluster_name" {
  description = "Name of the control plane EKS cluster"
  type        = string
  default     = "ingress-cp-dev"
}

variable "cp_cluster_version" {
  description = "Kubernetes version for the control plane cluster"
  type        = string
  default     = "1.29"
}

variable "cp_node_instance_types" {
  description = "Instance types for control plane node group"
  type        = list(string)
  default     = ["t3.medium"]
}

variable "cp_node_min_size" {
  description = "Minimum number of nodes in CP cluster"
  type        = number
  default     = 2
}

variable "cp_node_max_size" {
  description = "Maximum number of nodes in CP cluster"
  type        = number
  default     = 4
}

variable "cp_node_desired_size" {
  description = "Desired number of nodes in CP cluster"
  type        = number
  default     = 2
}

################################################################################
# EKS Data Plane Clusters (N-region design)
################################################################################

variable "dp_clusters" {
  description = "Map of data plane cluster configurations, keyed by region identifier"
  type = map(object({
    cluster_name    = string
    cluster_version = optional(string, "1.29")
    instance_types  = optional(list(string), ["c5.large"])
    min_size        = optional(number, 2)
    max_size        = optional(number, 20)
    desired_size    = optional(number, 2)
    node_labels     = optional(map(string), {})
  }))
  default = {
    primary = {
      cluster_name = "ingress-dp-dev"
    }
  }
}

################################################################################
# RDS
################################################################################

variable "rds_instance_class" {
  description = "RDS instance class"
  type        = string
  default     = "db.t3.medium"
}

variable "rds_engine_version" {
  description = "PostgreSQL engine version"
  type        = string
  default     = "16"
}

variable "rds_multi_az" {
  description = "Enable Multi-AZ for RDS"
  type        = bool
  default     = false
}

variable "rds_allocated_storage" {
  description = "Allocated storage for RDS in GB"
  type        = number
  default     = 20
}

variable "rds_db_name" {
  description = "Name of the initial database"
  type        = string
  default     = "ingress"
}
