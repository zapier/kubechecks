# Argo CD Generators
This directory contains the code for Argo CD generators. Generators dynamically create Kubernetes resources from various sources of truth, such as Kustomize, Helm, Ksonnet, and others. They are a key component in Argo CD for automating resource creation and management.

## Overview
Generators in Argo CD enable the dynamic generation of Kubernetes manifests based on the desired state defined in different configurations. By leveraging these generators, Argo CD can efficiently manage and deploy resources across different environments.

## Why Forked?
This code is a fork of the Argo CD (v2.12) generator code. The fork was necessary due to an incompatibility between Kubechecks' use of the go-gitlab library and Argo CD's generator code. To resolve this, the generator code has been forked and adapted for compatibility with Kubechecks.

## Supported Generators
* Lists
* Clusters

## Unsupported Generators
* Git
* Pull Requests

## Usage
You can use these generators to automate the creation and management of Kubernetes resources in your environment, ensuring consistency and repeatability.
