package k8s.security

import data.lib.kubernetes

# https://kubesec.io/basics/containers-securitycontext-capabilities-add-index-sys-admin/
warn[msg] {
	kubernetes.containers[container]
	kubernetes.added_capability(container, "CAP_SYS_ADMIN")
	msg = kubernetes.format(sprintf("%s in the %s %s has SYS_ADMIN capabilties", [container.name, kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/containers-securitycontext-capabilities-drop-index-all/
# warn[msg] {
# 	kubernetes.containers[container]
# 	not kubernetes.dropped_capability(container, "all")
# 	msg = kubernetes.format(sprintf("%s in the %s %s doesn't drop all capabilities", [container.name, kubernetes.kind, kubernetes.name]))
# }

# https://kubesec.io/basics/containers-securitycontext-privileged-true/
warn[msg] {
	kubernetes.containers[container]
	container.securityContext.privileged
	msg = kubernetes.format(sprintf("%s in the %s %s is privileged", [container.name, kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/containers-securitycontext-readonlyrootfilesystem-true/
# warn[msg] {
# 	kubernetes.containers[container]
# 	kubernetes.no_read_only_filesystem(container)
# 	msg = kubernetes.format(sprintf("%s in the %s %s is not using a read only root filesystem", [container.name, kubernetes.kind, kubernetes.name]))
# }

# warn[msg] {
# 	kubernetes.containers[container]
# 	kubernetes.priviledge_escalation_allowed(container)
# 	msg = kubernetes.format(sprintf("%s in the %s %s allows priviledge escalation", [container.name, kubernetes.kind, kubernetes.name]))
# }

# https://kubesec.io/basics/containers-securitycontext-runasnonroot-true/
warn[msg] {
	kubernetes.containers[container]
	not container.securityContext.runAsNonRoot = true
	not container.securityContext.runAsUser > 0
	msg = kubernetes.format(sprintf("%s in the %s %s is running as root", [container.name, kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/containers-securitycontext-runasuser/
# warn[msg] {
# 	kubernetes.containers[container]
# 	container.securityContext.runAsUser < 10000
# 	msg = kubernetes.format(sprintf("%s in the %s %s has a UID of less than 10000", [container.name, kubernetes.kind, kubernetes.name]))
# }

# https://kubesec.io/basics/spec-hostaliases/
warn[msg] {
	kubernetes.pods[pod]
	pod.spec.hostAliases
	msg = kubernetes.format(sprintf("The %s %s is managing host aliases", [kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/spec-hostipc/
warn[msg] {
	kubernetes.pods[pod]
	pod.spec.hostIPC
	msg = kubernetes.format(sprintf("%s %s is sharing the host IPC namespace", [kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/spec-hostnetwork/
warn[msg] {
	kubernetes.pods[pod]
	pod.spec.hostNetwork
	msg = kubernetes.format(sprintf("The %s %s is connected to the host network", [kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/spec-hostpid/
warn[msg] {
	kubernetes.pods[pod]
	pod.spec.hostPID
	msg = kubernetes.format(sprintf("The %s %s is sharing the host PID", [kubernetes.kind, kubernetes.name]))
}

# https://kubesec.io/basics/spec-volumes-hostpath-path-var-run-docker-sock/
warn[msg] {
	kubernetes.volumes[volume]
	volume.hostpath.path = "/var/run/docker.sock"
	msg = kubernetes.format(sprintf("The %s %s is mounting the Docker socket", [kubernetes.kind, kubernetes.name]))
}
