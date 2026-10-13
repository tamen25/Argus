variable "region" {
  description = "AWS region"
  type        = string
  default     = "us-east-1"
}

variable "name" {
  description = "Prefix for every resource name"
  type        = string
  default     = "argus-bench"
}

variable "instance_type" {
  description = "8 vCPU / 32 GB holds kind with LGTM, the OpenTelemetry demo and Chaos Mesh; the agent's model runs elsewhere"
  type        = string
  default     = "m6i.2xlarge"
}

variable "volume_gb" {
  description = "Root volume size (container images and telemetry)"
  type        = number
  default     = 100
}

variable "repo_url" {
  description = "Argus repository to clone"
  type        = string
  default     = "https://github.com/tamen25/Argus.git"
}

variable "git_ref" {
  description = "Commit, tag or branch to run: recorded as the snapshot the results came from"
  type        = string
  default     = "main"
}

# Tool versions match the dev environment (WSL), so the VM is the same kind setup.

variable "go_version" {
  type    = string
  default = "1.26.9"
}

variable "kind_version" {
  type    = string
  default = "v0.32.0"
}

variable "kubectl_version" {
  type    = string
  default = "v1.36.2"
}

variable "helm_version" {
  type    = string
  default = "v3.21.3"
}

variable "codex_version" {
  description = "OpenAI Codex CLI version (npm @openai/codex)"
  type        = string
  default     = "0.142.5"
}
