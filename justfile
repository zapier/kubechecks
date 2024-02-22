help:
  @just --list

system_check:
	#!/usr/bin/env python3
	import os
	import subprocess
	devnull = open(os.devnull, 'w')
	check = "\u2713"
	cross = "\u2717"
	space = "\t"
	FAIL  = 0
	print("Checking dependencies:")

	print(f"...Minkube{space}: ", end="")
	try:
		mini_version = subprocess.check_output("minikube version".split()).decode().split("\n")[0].strip()
		print(f"{check} {mini_version}")
	except:
		FAIL += 1
		print(f"{cross} NOT FOUND")

	print(f"...GoLang{space}: ", end="")
	try:
		go_version = subprocess.check_output("go version".split()).decode().strip()
		print(f"{check} {go_version}")
	except:
		FAIL += 1
		print(f"{cross} NOT FOUND")

	print(f"...Ngrok{space}: ", end="")
	try:
		ngrok_version = subprocess.check_output("ngrok version".split()).decode().strip()
		print(f"{check} {ngrok_version}")
		subprocess.check_output("ngrok config check".split()).decode().strip()
	except:
		FAIL += 1
		print(f"{cross} NOT FOUND")

	if FAIL > 0:
		print(f"\n\u274c {FAIL} of the dependency and configuration checks have failed.\n")
		exit(FAIL)

cluster_up:
	minikube start --driver=docker --addons=dashboard,ingress --cpus='4' --memory='12g' --nodes=1

cluster_down:
	minikube delete

# Creates a minikube tunnel
cluster_tunnel:
	minikube tunnel

# Starts a minikube cluster and tilt
start: system_check cluster_up
	tilt up

dump_crds:
	cd tools/dump_crds/; go mod tidy; go run -v dump_crds.go ../../schemas

unit_test:
	go test ./...

unit_test_race:
	go test -race ./...

rebuild_docs:
    earthly +rebuild-docs

lint-golang:
    #!/usr/bin/env bash
    earthly +lint-golang --GOLANGCI_LINT_VERSION=$(mise current golangci-lint)
