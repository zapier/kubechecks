# kubechecks - Kubernetes Manifest Linter

## Check your Kubernetes manifests before it hits the cluster

You're deep into developing new projects for your Kubernetes cluster. Local testing shows all green. Unit tests? Passing.
You merge your changes into your main branch... and ArgoCD starts showing red. Sound familiar?

This is where `kubechecks` enters the picture. `kubechecks` is a handy tool that detects changes between your live deployments
managed via ArgoCD and changes made in your PR/MRs; letting you know _before_ you merge that branch what will change. On top of
that, `kubechecks` runs handy linting reports from [`kubepug`](https://github.com/rikatz/kubepug), [`kubeconform`](https://github.com/yannh/kubeconform), and [`conftest`](https://www.conftest.dev/) to build an even better picture of those changes.

Think of it like `terraform apply` but for Kubernetes; with `kubechecks`, you'll have greater confidence than ever before in your changes.

## Why kubechecks?

kubechecks was built out a desire to simplify the amount of separate pipelines required to be run for pull requests at Zapier. We've
been using it internally for <XYZ>, and we think it's pretty great; and we hope you find it useful, too!

Some great features:

- Supports Github and Gitlab.
- Clear visibility into what new commits will actually change against your live applications
- Validate your manifests are production-ready via multiple conformance checks
- (Coming soon) works in Github Action pipelines!

## Documentation

To learn more about kubechecks [see our documentation](https://kubechecks.readthedocs.io/).
