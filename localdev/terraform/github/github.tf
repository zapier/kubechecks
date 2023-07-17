// Set up a new repository with a README.md file
resource "github_repository" "kubechecks" {
  name        = local.random_pet != "" ? local.random_pet : "kubechecks"
  description = "Kubechecks demo repository"
  auto_init   = true
  visibility  = "public"
}

// Set the main branch as default
resource "github_branch" "main" {
  repository = github_repository.kubechecks.name
  branch     = "main"
  depends_on = [
    github_repository.kubechecks
  ]
}

resource "github_branch_default" "default" {
  repository = github_repository.kubechecks.name
  branch     = github_branch.main.branch
  depends_on = [
    # delay creating this until initial files have been uploaded
    github_branch.main
  ]
}

resource "local_file" "github_project" {
  filename = "project.url"
  content  = github_repository.kubechecks.html_url

  depends_on = [
    # delay creating this until initial files have been uploaded
    github_repository_file.base_files
  ]
}

resource "github_repository_file" "base_files" {
  for_each = module.vcs_files.base_files

  repository          = github_repository.kubechecks.name
  file                = each.key
  overwrite_on_create = true
  branch              = "main"
  content             = each.value
  commit_message      = "add ${each.key} file"

  depends_on = [
    # delay creating this until we actually have a main branch
    github_branch_default.default
  ]
}

locals {
  base_files_length   = length(keys(module.vcs_files.base_files))
  base_files_last_key = keys(module.vcs_files.base_files)[local.base_files_length - 1]
}

data "github_branch" "head" {
  branch     = "main"
  repository = github_repository.kubechecks.name

  depends_on = [github_repository_file.base_files]
}

/* ---------------------- */
// Create 6 branches for 6 possible PRs

resource "github_branch" "pr1_change" {
  repository = github_repository.kubechecks.name
  branch     = "pr1-change"
  source_sha = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr1_change" {
  for_each = module.vcs_files.mr_files[1]

  repository          = github_repository.kubechecks.name
  file                = each.key
  branch              = github_branch.pr1_change.branch
  content             = each.value
  commit_message      = "pr1 - update ${each.key}"
  overwrite_on_create = true
}

// ----------------------

resource "github_branch" "pr2_change" {
  repository = github_repository.kubechecks.name
  branch     = "pr2-change"
  source_sha = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr2_change" {
  for_each = module.vcs_files.mr_files[2]

  repository          = github_repository.kubechecks.name
  file                = each.key
  branch              = github_branch.pr2_change.branch
  content             = each.value
  commit_message      = "pr2 - update ${each.key}"
  overwrite_on_create = true
}

// ----------------------

resource "github_branch" "pr3_change" {
  repository = github_repository.kubechecks.name
  branch    = "pr3-change"
  source_sha     = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr3_change" {
  for_each = module.vcs_files.mr_files[3]

  repository             = github_repository.kubechecks.name
  file           = each.key
  branch              = github_branch.pr3_change.branch
  content             = each.value
  commit_message      = "pr3 - update ${each.key}"
  overwrite_on_create = true
}

// ----------------------

resource "github_branch" "pr4_change" {
  repository = github_repository.kubechecks.name
  branch    = "pr4-change"
  source_sha     = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr4_change" {
  for_each = module.vcs_files.mr_files[4]

  repository             = github_repository.kubechecks.name
  file           = each.key
  branch              = github_branch.pr4_change.branch
  content             = each.value
  commit_message      = "pr4 - update ${each.key}"
  overwrite_on_create = true
}

// ----------------------

resource "github_branch" "pr5_change" {
  repository = github_repository.kubechecks.name
  branch    = "pr5-change"
  source_sha     = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr5_change" {
  for_each = module.vcs_files.mr_files[5]

  repository             = github_repository.kubechecks.name
  file           = each.key
  branch              = github_branch.pr5_change.branch
  content             = each.value
  commit_message      = "pr5 - update ${each.key}"
  overwrite_on_create = true
}

// ----------------------

resource "github_branch" "pr6_change" {
  repository = github_repository.kubechecks.name
  branch    = "pr6-change"
  source_sha     = data.github_branch.head.sha

  depends_on = [github_repository_file.base_files]
}

resource "github_repository_file" "pr6_change" {
  for_each = module.vcs_files.mr_files[6]

  repository             = github_repository.kubechecks.name
  file           = each.key
  branch              = github_branch.pr6_change.branch
  content             = each.value
  commit_message      = "pr6 - update ${each.key}"
  overwrite_on_create = true
}
