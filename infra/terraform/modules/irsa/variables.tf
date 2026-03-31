variable "role_name" {
  description = "Name of the IAM role to create"
  type        = string
}

variable "oidc_provider_arn" {
  description = "ARN of the EKS cluster OIDC provider"
  type        = string
}

variable "oidc_provider_url" {
  description = "URL of the OIDC provider (without https://)"
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace for the service account"
  type        = string
}

variable "service_account_name" {
  description = "Name of the Kubernetes service account"
  type        = string
}

variable "policy_arns" {
  description = "List of IAM policy ARNs to attach to the role"
  type        = list(string)
  default     = []
}

variable "inline_policies" {
  description = "Map of inline policy names to policy JSON documents"
  type        = map(string)
  default     = {}
}

variable "max_session_duration" {
  description = "Maximum session duration in seconds (1-12 hours)"
  type        = number
  default     = 3600
}

variable "tags" {
  description = "Additional tags to apply to all resources"
  type        = map(string)
  default     = {}
}
