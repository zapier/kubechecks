locals {
  child_tfvars = <<EOF
parent_vars = {
  random_pet="${random_pet.random_name.id}",
}
EOF

  child_tf_dirs = [
    "gitlab",
    "github",
  ]
}

resource "random_pet" "random_name" {
  separator = "-"
  length    = 2
}

output "random_pet" {
  value = random_pet.random_name.id
}

resource "local_file" "child_tfvars" {
  for_each = toset(local.child_tf_dirs)

  filename = "${each.value}/parent.auto.tfvars"
  content = local.child_tfvars
}


terraform {
  required_providers {
    tfe = {
      source  = "hashicorp/tfe"
      version = "0.31.0"
    }
  }
}
