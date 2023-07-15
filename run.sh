#!/usr/bin/env bash

ARGOCD_TOKEN="eyJhbGciOiJIU..........zXyiHBcMt7dB2dE"
GITHUB_TOKEN="ghp_xyz......1hJ"

go run ./main.go \
  --vcs-type github \
  controller \
    --enable-conftest \
    --ensure-webhooks \
    --monitor-all-applications \
    --show-debug-info \
    --webhook-secret omgzwtfbbq \
    --webhook-url-base https://kubechecks.thehideaway.social \
    --argocd-api-server-addr https://argocd.thehideaway.social \
    --argocd-api-token omgzwtfbbq \
    --log-level debug \
    --vcs-token "$GITHUB_TOKEN"
