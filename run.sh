#!/usr/bin/env bash

ARGOCD_TOKEN="eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcmdvY2QiLCJzdWIiOiJhZG1pbjphcGlLZXkiLCJleHAiOjE2ODkzNzQyNjUsIm5iZiI6MTY4OTI4Nzg2NSwiaWF0IjoxNjg5Mjg3ODY1LCJqdGkiOiJrdWJlY2hlY2tzLXRlc3RpbmcifQ.RvdOi0J88aG0mUUxDZBnk9DY_UD_zXyiHBcMt7dB2dE"
GITHUB_TOKEN="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"

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
