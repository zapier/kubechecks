output "gitlab_project_url" {
  value = <<EOF
${gitlab_project.kubechecks_test_project.web_url}
EOF
}

output "gitlab_project_name" {
  value = gitlab_project.kubechecks_test_project.path_with_namespace
}

#output "main_commits" {
#  value = data.gitlab_branch.head.commit
#}

#output "base_files" {
#  value = module.vcs_files.base_files
#}
#
#output "mr1_files" {
#  value = module.vcs_files.mr_files
#}