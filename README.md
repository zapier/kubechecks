# Kubechecks Helm Charts

First, you need to get the repo added to helm!

```sh
helm repo add kubechecks https://zapier.github.io/kubechecks/
helm repo update
helm search repo kubechecks -l
```

Then you can install it:

```sh
helm install kubechecks kubechecks/kubechecks
```
✕