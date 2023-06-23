#!/usr/bin/env bash
DIR=$1

echo "#######################"
echo " helm-unittest.sh ${DIR}"
echo "#######################"

###############################################################################
# NAME: Detect Helm Version
if [[ `grep -R "apiVersion: v2" "$1/Chart.yaml" > /dev/null; echo $?` -eq 0 ]]
then
  HELM_VER="--helm3"
else
  HELM_VER=""
fi

helm unittest ${HELM_VER} "${1}"