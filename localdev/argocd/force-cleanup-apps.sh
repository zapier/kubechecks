#!/usr/bin/env bash

# check if Applications CRD still exists
exists=$(kubectl get crd | grep "applications.argoproj.io" | wc -l)
if [ "$exists" -eq 0 ]; then
  echo "Applications CRD doesn't exist. Exiting..."
  exit 0;
fi

# give time for other processes to cleanup properly
echo "ArgoCD Cleanup: waiting 20 seconds..."
sleep 20
echo "Cleaning up ArgoCD test Applications and CRDs..."

# Cleanup Applications
for a in $(kubectl get application -n kubechecks -o=jsonpath='{.items[*].metadata.name}'); do
  # remove finalizer from Applications (ArgoCD is probably shutdown by now and deleting apps will hang)
  kubectl patch application $a -n kubechecks  --type json -p='[{"op": "remove", "path": "/metadata/finalizers"}]';
  kubectl delete application $a -n kubechecks;
done;

# Cleanup ArgoCD CRDs
for crd in applications.argoproj.io applicationsets.argoproj.io appprojects.argoproj.io; do
  kubectl delete crd $crd;
done;

exit 0;