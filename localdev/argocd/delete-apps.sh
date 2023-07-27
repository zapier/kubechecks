#!/usr/bin/env bash

echo "Deleting ArgoCD test Applications & AppSets (gracefully)..."

# Delete ApplicationSets
for a in $(kubectl get ApplicationSet -n kubechecks -o=jsonpath='{.items[*].metadata.name}'); do
  kubectl delete ApplicationSet $a -n kubechecks --timeout=10s;
done;

# Delete Applications
for a in $(kubectl get application -n kubechecks -o=jsonpath='{.items[*].metadata.name}'); do
  kubectl delete application $a -n kubechecks --timeout=10s;
done;

exit 0;