#!/usr/bin/env bash

KUBE_VERSION=$1
DIR=$2

echo "#######################"
echo " kubeconform.sh ${DIR}"
echo "#######################"

if [ ! "$KUBE_VERSION" ] || [ ! "$DIR" ]; then
  echo "usage: $0 KUBE_VERSION DIR"
  exit 1
fi

if [ ! -d "${DIR}" ]; then
  echo "error: ${DIR} not found"
  exit 0
fi

function testFile () {
  dir=$1
  file=$2

  if [ -n "$file" ]; then
    file="-f${file}"
  fi

  if ! helm template "${file}" promagg "${dir}" \
    | kubeconform \
      -strict \
      -kubernetes-version "${KUBE_VERSION}" \
      -summary \
      -verbose \
      -schema-location default \
      -schema-location 'https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'; then
    return 1
  fi
  return 0
}

HAS_FAILING_TEST=0

# Run a check on default values.
echo "## Running kubeconform against $KUBE_VERSION with ./values.yaml"
if ! testFile "${DIR}" "${DIR}/values.yaml"; then
  HAS_FAILING_TEST=1
fi

if [ -d "${DIR}/ci" ]; then
  FILES="${DIR}/ci/*"
  for FILE in $FILES; do
    echo "## Running kubeconform against $KUBE_VERSION with ${FILE}"
    if ! testFile "${DIR}" "${FILE}"; then
      HAS_FAILING_TEST=1
    fi
  done
fi

exit ${HAS_FAILING_TEST}