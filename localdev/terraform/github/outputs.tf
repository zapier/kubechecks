output "github_project_url" {
  value = <<EOF
${github_repository.kubechecks.html_url}
EOF
}

output "github_repo_name" {
  value = github_repository.kubechecks.name
}