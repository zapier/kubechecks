#!/usr/bin/env bash

set -e

exitCode=0
message=$1

# Log the actual diff for debugging purposes
git diff --name-only | cat
if ! git diff --exit-code --quiet; then
  echo "$message"
  exitCode=1
fi

exit $exitCode
