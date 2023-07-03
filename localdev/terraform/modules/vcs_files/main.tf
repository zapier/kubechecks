locals {
  base_files_list = fileset("${path.module}/base_files", "**/*")
}

output "base_files" {
  value = {
    for f in local.base_files_list : f => file("${path.module}/base_files/${f}")
  }
}

output "mr_files" {
  value = {
    1 = {
      for f in fileset("${path.module}/mr1_files", "**/*") : f => file("${path.module}/mr1_files/${f}")
    }
    2 = {
      for f in fileset("${path.module}/mr2_files", "**/*") : f => file("${path.module}/mr2_files/${f}")
    }
    3 = {
      for f in fileset("${path.module}/mr3_files", "**/*") : f => file("${path.module}/mr3_files/${f}")
    }
    4 = {
      for f in fileset("${path.module}/mr4_files", "**/*") : f => file("${path.module}/mr4_files/${f}")
    }
    5 = {
      for f in fileset("${path.module}/mr5_files", "**/*") : f => file("${path.module}/mr5_files/${f}")
    }
    6 = {
      for f in fileset("${path.module}/mr6_files", "**/*") : f => file("${path.module}/mr6_files/${f}")
    }
  }
}
