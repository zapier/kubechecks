VERSION 0.7

ARG --global USERARCH

test:
    BUILD +ci-golang
    BUILD +ci-helm

ci-golang:
    BUILD +fmt-golang
    BUILD +golang-ci-lint
    BUILD +validate-golang
    BUILD +test-golang

ci-helm:
    BUILD +test-helm
    BUILD +release-helm

docker-multiarch:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +docker

release:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +docker
    BUILD +release-helm

go-deps:
    ARG GOOS=linux
    ARG GOARCH=$USERARCH
    ARG --required GOLANG_VERSION

    FROM --platform=linux/$USERARCH golang:$GOLANG_VERSION

    ENV GO111MODULE=on
    ENV CGO_ENABLED=0

    WORKDIR /src
    COPY go.mod go.sum /src
    RUN go mod download
    SAVE ARTIFACT go.mod AS LOCAL go.mod
    SAVE ARTIFACT go.sum AS LOCAL go.sum

rebuild-docs:
    FROM +go-deps

    COPY . /src
    RUN go run hacks/env-to-docs.go

    SAVE ARTIFACT ./docs/usage.md AS LOCAL ./docs/usage.md

validate-golang:
    FROM +go-deps

    WORKDIR /src
    COPY . /src
    RUN go vet ./...

test-golang:
    FROM +go-deps

    WORKDIR /src
    COPY . /src

    RUN go test ./...

build-binary:
    ARG GOOS=linux
    ARG GOARCH=$USERARCH
    ARG VARIANT
    ARG --required GIT_TAG
    ARG --required GIT_COMMIT

    FROM --platform=linux/$USERARCH +go-deps

    WORKDIR /src
    COPY . /src
    RUN GOARM=${VARIANT#v} go build -ldflags "-X github.com/zapier/kubechecks/pkg.GitCommit=$GIT_COMMIT -X github.com/zapier/kubechecks/pkg.GitTag=$GIT_TAG" -o kubechecks
    SAVE ARTIFACT kubechecks

build-debug-binary:
    FROM --platform=linux/$USERARCH +go-deps
    WORKDIR /src
    COPY . /src
    RUN go build -gcflags="all=-N -l" -ldflags "-X github.com/zapier/kubechecks/pkg.GitCommit=$GIT_COMMIT -X github.com/zapier/kubechecks/pkg.GitTag=$GIT_TAG" -o kubechecks
    SAVE ARTIFACT kubechecks

docker:
    ARG TARGETPLATFORM
    ARG TARGETARCH
    ARG TARGETVARIANT

    FROM --platform=$TARGETPLATFORM ubuntu:25.10
    RUN apt update && apt install -y ca-certificates curl git

    WORKDIR /tmp
    ARG KUSTOMIZE_VERSION=5.6.0
    RUN \
        curl \
            --fail \
            --silent \
            --show-error \
            --location \
            "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" \
            --output install_kustomize.sh && \
        chmod 700 install_kustomize.sh && \
        ./install_kustomize.sh ${KUSTOMIZE_VERSION} /usr/local/bin

    ARG HELM_VERSION=3.10.0
    RUN \
        curl \
            --fail \
            --silent \
            --show-error \
            --location \
            "https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3" \
            --output get-helm-3.sh && \
        chmod 700 get-helm-3.sh && \
        ./get-helm-3.sh -v v${HELM_VERSION}

    RUN mkdir /app

    WORKDIR /app

    VOLUME /app/policies
    VOLUME /app/schemas

    COPY (+build-binary/kubechecks --platform=linux/$USERARCH --GOARCH=$TARGETARCH --VARIANT=$TARGETVARIANT) .
    RUN ./kubechecks help

    CMD ["./kubechecks", "controller"]
    ARG --required IMAGE_NAME
    ARG LATEST_IMAGE_NAME=""
    SAVE IMAGE --push $IMAGE_NAME $LATEST_IMAGE_NAME

dlv:
    ARG --required GOLANG_VERSION

    FROM golang:$GOLANG_VERSION

    RUN apt update && apt install -y ca-certificates curl git
    RUN go install github.com/go-delve/delve/cmd/dlv@latest

    SAVE ARTIFACT /go/bin/dlv

docker-debug:
    FROM +docker --GIT_TAG=debug --GIT_COMMIT=abcdef

    COPY (+build-debug-binary/kubechecks --GOARCH=$USERARCH --VARIANT=$TARGETVARIANT) .

    COPY (+dlv/dlv --GOARCH=$USERARCH --VARIANT=$TARGETVARIANT) /usr/local/bin/dlv

    CMD ["/usr/local/bin/dlv", "--listen=:2345", "--api-version=2", "--headless=true", "--accept-multiclient", "exec", "--continue", "./kubechecks", "controller"]

    ARG IMAGE_NAME="kubechecks:debug"
    SAVE IMAGE --push $IMAGE_NAME

fmt-golang:
    FROM +go-deps

    WORKDIR /src
    COPY . /src

    RUN go fmt \
        && ./hacks/exit-on-changed-files.sh

golang-ci-lint:
    ARG --required GOLANGCI_LINT_VERSION

    FROM golangci/golangci-lint:v${GOLANGCI_LINT_VERSION}

    WORKDIR /src
    COPY . .

    RUN golangci-lint --timeout 15m run --verbose

test-helm:
    ARG CHART_TESTING_VERSION="3.7.1"
    FROM quay.io/helmpack/chart-testing:v${CHART_TESTING_VERSION}

    # install kubeconform
    ARG KUBECONFORM_VERSION="0.7.0"
    RUN FILE=kubeconform.tgz \
        && URL=https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-${USERARCH}.tar.gz \
        && wget ${URL} \
            --output-document ${FILE} \
        && tar \
            --extract \
            --verbose \
            --directory /bin \
            --file ${FILE} \
        && kubeconform -v

    ARG HELM_UNITTEST_VERSION="0.3.3"
    RUN apk add --no-cache bash git \
        && helm plugin install --version "${HELM_UNITTEST_VERSION}" https://github.com/helm-unittest/helm-unittest \
        && helm unittest --help

    # actually lint the chart
    WORKDIR /src
    COPY . /src
    RUN git fetch --prune --unshallow | true
    RUN ct --config ./.github/ct.yaml lint ./charts

release-helm:
    ARG CHART_RELEASER_VERSION="1.6.0"
    FROM quay.io/helmpack/chart-releaser:v${CHART_RELEASER_VERSION}

    ARG HELM_VERSION="3.8.1"
    RUN FILE=helm.tgz \
        && URL=https://get.helm.sh/helm-v${HELM_VERSION}-linux-${USERARCH}.tar.gz \
        && wget ${URL} \
            --output-document ${FILE} \
        && tar \
            --strip-components=1 \
            --extract \
            --verbose \
            --directory /bin \
            --file ${FILE} \
        && helm version

    WORKDIR /src
    COPY . /src
    RUN cr --config .github/cr.yaml package charts/*
    SAVE ARTIFACT .cr-release-packages/ AS LOCAL ./dist

    RUN mkdir -p .cr-index
    RUN git config --global user.email "opensource@zapier.com"
    RUN git config --global user.name "Open Source at Zapier"
    RUN git fetch --prune --unshallow | true

    ARG repo_owner=""
    ARG token=""
    RUN --push cr --config .github/cr.yaml upload --owner $repo_owner --token $token --skip-existing
    RUN --push cr --config .github/cr.yaml index --owner $repo_owner --token $token --push
