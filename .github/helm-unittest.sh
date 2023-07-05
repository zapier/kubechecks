#!/usr/bin/env bash
DIR=$1

echo "#######################"
echo " helm-unittest.sh ${DIR}"
echo "#######################"

###############################################################################
# We always use Helm 3 as Helm 2 is now deprecated
helm unittest "${1}"