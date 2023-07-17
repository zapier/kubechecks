resource "gitlab_project" "kubechecks_test_project" {
  name             = local.random_pet
  visibility_level = "public"

  only_allow_merge_if_pipeline_succeeds = true
}

resource "local_file" "gitlab_project" {
  filename = "project.url"
  content  = gitlab_project.kubechecks_test_project.http_url_to_repo

  depends_on = [
    # delay creating this until initial files have been uploaded
    gitlab_repository_file.base_files
  ]
}

resource "gitlab_label" "kubechecks_production" {
  project     = gitlab_project.kubechecks_test_project.id
  name        = "kubechecks:production"
  description = ""
  color       = "#dc143c"
}

resource "gitlab_label" "kubechecks_staging" {
  project     = gitlab_project.kubechecks_test_project.id
  name        = "kubechecks:staging"
  description = ""
  color       = "#00b140"
}

resource "gitlab_repository_file" "base_files" {
  for_each = module.vcs_files.base_files

  project        = gitlab_project.kubechecks_test_project.id
  file_path      = each.key
  branch         = "main"
  content        = base64encode(each.value)
  commit_message = "add ${each.key} file"
}

locals {
  base_files_length   = length(keys(module.vcs_files.base_files))
  base_files_last_key = keys(module.vcs_files.base_files)[local.base_files_length - 1]
}

data "gitlab_branch" "head" {
  name    = "main"
  project = gitlab_project.kubechecks_test_project.id

  depends_on = [gitlab_repository_file.base_files]
}

// -------------------------------------------------------------------------------

resource "gitlab_branch" "mr1_change" {
  name    = "mr1-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id

  depends_on = [gitlab_repository_file.base_files]
}

resource "gitlab_repository_file" "mr1_change" {
  for_each = module.vcs_files.mr_files[1]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr1_change.name
  content             = base64encode(each.value)
  commit_message      = "mr1 - update ${each.key}"
  overwrite_on_create = true
}

// -------------------------------------------------------------------------------

resource "gitlab_branch" "mr2_change" {
  name    = "mr2-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id
}

resource "gitlab_repository_file" "mr2_change" {
  for_each = module.vcs_files.mr_files[2]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr2_change.name
  content             = base64encode(each.value)
  commit_message      = "mr2 - update ${each.key}"
  overwrite_on_create = true
}

// -------------------------------------------------------------------------------

resource "gitlab_branch" "mr3_change" {
  name    = "mr3-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id
}

resource "gitlab_repository_file" "mr3_change" {
  for_each = module.vcs_files.mr_files[3]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr3_change.name
  content             = base64encode(each.value)
  commit_message      = "mr3 - update ${each.key}"
  overwrite_on_create = true
}


resource "gitlab_branch" "mr4_change" {
  name    = "mr4-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id
}

resource "gitlab_repository_file" "mr4_change" {
  for_each = module.vcs_files.mr_files[4]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr4_change.name
  content             = base64encode(each.value)
  commit_message      = "mr4 - update ${each.key}"
  overwrite_on_create = true
}



resource "gitlab_branch" "mr5_change" {
  name    = "mr5-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id
}

resource "gitlab_repository_file" "mr5_change" {
  for_each = module.vcs_files.mr_files[5]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr5_change.name
  content             = base64encode(each.value)
  commit_message      = "mr5 - update ${each.key}"
  overwrite_on_create = true
}

resource "gitlab_branch" "mr6_change" {
  name    = "mr6-change"
  ref     = tolist(data.gitlab_branch.head.commit)[0].id
  project = gitlab_project.kubechecks_test_project.id
}

resource "gitlab_repository_file" "mr6_change" {
  for_each = module.vcs_files.mr_files[6]

  project             = gitlab_project.kubechecks_test_project.id
  file_path           = each.key
  branch              = gitlab_branch.mr6_change.name
  content             = base64encode(each.value)
  commit_message      = "mr6 - update ${each.key}"
  overwrite_on_create = true
}
