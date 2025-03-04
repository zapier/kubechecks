#!/usr/bin/env bash

to_echo() {
    if [ "$1" -eq 1 ]; then
        echo "$2"
    fi
}

read_tool_versions_write_to_env() {
    local -r tool_versions_file="$1"
    cat $tool_versions_file
    # loop over each line of the .tool-versions file
    while read -r line; do
        # split the line into a bash array using the default space delimeter
        IFS=" " read -r -a lineArray <<<"$line"

        # get the key and value from the array, set the key to all uppercase
        key="${lineArray[0],,}"
        value="${lineArray[1]}"

        # ignore comments, comments always start with #
        if [[ ${key:0:1} != "#" ]]; then
            full_key="${key/-/_}_tool_version"
            export "${full_key/-/_}=${value}"
        fi
    done <"$tool_versions_file"
}

read_tool_versions_write_to_env '.tool-versions'

set -x

# shellcheck disable=SC2048
earthly $* \
  --CHART_RELEASER_VERSION=${helm_cr_tool_version} \
  --GOLANG_VERSION=${golang_tool_version} \
  --GOLANGCI_LINT_VERSION=${golangci_lint_tool_version} \
  --HELM_VERSION=${helm_tool_version} \
  --KUBECONFORM_VERSION=${kubeconform_tool_version} \
  --KUSTOMIZE_VERSION=${kustomize_tool_version} \
  --GIT_COMMIT=$(git rev-parse --short HEAD) \
  --KUBECHECKS_LOG_LEVEL=debug
