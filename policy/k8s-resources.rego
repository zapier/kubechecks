package k8s.resources

import data.lib.kubernetes

# https://kubesec.io/basics/containers-resources-limits-memory
violation[msg] {
	kubernetes.containers[container]
	not container.resources.limits.memory
	msg = kubernetes.format(sprintf("%s in the %s %s does not have a memory limit set", [container.name, kubernetes.kind, kubernetes.name]))
}

violation[msg] {
	kubernetes.containers[container]
	not container.resources.requests.memory
	msg = kubernetes.format(sprintf("%s in the %s %s does not have a memory request set", [container.name, kubernetes.kind, kubernetes.name]))
}

violation[msg] {
	kubernetes.containers[container]
	not container.resources.requests.cpu
	msg = kubernetes.format(sprintf("%s in the %s %s does not have a CPU request set", [container.name, kubernetes.kind, kubernetes.name]))
}

# Validate the requested CPU is less than CPU limit.
violation[msg] {
	kubernetes.containers[container]
	container.resources.limits.cpu
	request := kubernetes.canonify_cpu(container.resources.requests.cpu)
	limit := kubernetes.canonify_cpu(container.resources.limits.cpu)
	request > limit
	msg = kubernetes.format(sprintf("%s in the %s %s requests more CPU (%s) than it's limit (%s)", [container.name, kubernetes.kind, kubernetes.name, container.resources.requests.cpu, container.resources.limits.cpu]))
}

# Validate requested memory is less than memory limit.
violation[msg] {
	kubernetes.containers[container]
	container.resources.limits.memory
	request := kubernetes.canonify_mem(container.resources.requests.memory)
	limit := kubernetes.canonify_mem(container.resources.limits.memory)
	request > limit
	msg = kubernetes.format(sprintf("%s in the %s %s requests more memory (%s) than it's limit (%s)", [container.name, kubernetes.kind, kubernetes.name, container.resources.requests.memory, container.resources.limits.memory]))
}