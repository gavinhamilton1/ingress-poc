################################################################################
# Dev Environment Defaults
################################################################################

aws_region  = "us-east-1"
environment = "dev"
project     = "ingress-poc"

# VPC
vpc_cidr           = "10.0.0.0/16"
single_nat_gateway = true

# EKS Control Plane
cp_cluster_name        = "ingress-cp-dev"
cp_cluster_version     = "1.29"
cp_node_instance_types = ["t3.medium"]
cp_node_min_size       = 2
cp_node_max_size       = 4
cp_node_desired_size   = 2

# EKS Data Plane Clusters
# Add additional entries to deploy DP clusters in additional regions/configs.
dp_clusters = {
  primary = {
    cluster_name = "ingress-dp-dev"
    instance_types = ["c5.large"]
    min_size       = 2
    max_size       = 20
    desired_size   = 2
    node_labels = {
      "ingress.io/role" = "dataplane"
      "ingress.io/region" = "primary"
    }
  }
  # Example: add a second DP cluster for multi-region
  # secondary = {
  #   cluster_name   = "ingress-dp-dev-secondary"
  #   instance_types = ["c5.large"]
  #   min_size       = 2
  #   max_size       = 10
  #   desired_size   = 2
  #   node_labels = {
  #     "ingress.io/role"   = "dataplane"
  #     "ingress.io/region" = "secondary"
  #   }
  # }
}

# RDS
rds_instance_class    = "db.t3.medium"
rds_engine_version    = "16"
rds_multi_az          = false
rds_allocated_storage = 20
rds_db_name           = "ingress"
