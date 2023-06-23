terraform {
  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 5.1"
    }
  }
}

# Configure the GitHub Provider
provider "github" {
  # Use GITHUB_TOKEN as an env var to auth
}

variable "parent_vars" {
  type    = map(string)
  default = {}
}

variable "kubechecks_github_hook_secret_key" {
  default = "asdf"
}

variable "ngrok_url" {
  default = "https://httpbin.org/post"
}

variable "kubecheck_webhook_prefix" {
  default = "kubechecks/hooks"
}

module "vcs_files" {
  source = "../modules/vcs_files"
}

locals {
  random_pet = try(var.parent_vars.random_pet, "")
}

# Make a backup of the settings provided by parent TF workspace
# If the parent is destroyed it will remove the tfvars file that this
# workspace would need to also do a destroy.
# TF loads the tfvars in alphabetical order, so the parent.auto.tfvars 
# will take precedence.
resource "local_file" "localdev_auto_tfvars" {
  filename = "localdev.auto.tfvars"
  content  = <<EOF
parent_vars=${format("%#v", var.parent_vars)}
EOF
}
