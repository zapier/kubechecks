# syntax=docker/dockerfile:1.4

# Build arguments for versions (read from .tool-versions)
ARG GOLANG_VERSION=1.25.5
ARG ALPINE_VERSION=3.21

# ============================================================================
# Stage: go-deps
# Cache Go module downloads with BuildKit cache mount
# Always runs on build platform (amd64) for fast cross-compilation
# ============================================================================
FROM --platform=$BUILDPLATFORM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS go-deps

WORKDIR /src

# Install git (needed for go mod download with private repos)
RUN apk add --no-cache git

# Enable Go modules and disable CGO for static binary
ENV GO111MODULE=on
ENV CGO_ENABLED=0

# Copy go.mod and go.sum first for better caching
COPY go.mod go.sum ./

# Download dependencies with BuildKit cache mount for speed
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# ============================================================================
# Stage: builder
# Compile production binary with aggressive size optimization
# Uses Go cross-compilation for fast multi-arch builds (no QEMU emulation!)
# ============================================================================
FROM go-deps AS builder

ARG GIT_COMMIT=unknown
ARG GIT_TAG=dev
# Docker build args for cross-compilation
ARG TARGETOS
ARG TARGETARCH

# Copy source code
COPY . .

# Build with:
# -trimpath: Remove file system paths from binary
# -w: Omit DWARF symbol table
# -s: Omit symbol table and debug info
# GOOS/GOARCH: Cross-compile for target platform (avoids slow QEMU emulation)
# Target binary size: 30-50MB (down from 223MB)
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -trimpath \
        -ldflags="-w -s -X github.com/zapier/kubechecks/pkg.GitCommit=${GIT_COMMIT} -X github.com/zapier/kubechecks/pkg.GitTag=${GIT_TAG}" \
        -o /out/kubechecks \
        .

# ============================================================================
# Stage: debug-builder
# Compile debug binary with symbols for delve debugging
# ============================================================================
FROM go-deps AS debug-builder

ARG GIT_COMMIT=debug
ARG GIT_TAG=debug
# Docker build args for cross-compilation
ARG TARGETOS
ARG TARGETARCH

# Copy source code
COPY . .

# Build with debug symbols:
# -gcflags="all=-N -l": Disable optimizations and inlining for debugging
# GOOS/GOARCH: Cross-compile for target platform
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
        -gcflags="all=-N -l" \
        -ldflags="-X github.com/zapier/kubechecks/pkg.GitCommit=${GIT_COMMIT} -X github.com/zapier/kubechecks/pkg.GitTag=${GIT_TAG}" \
        -o /out/kubechecks \
        .

# Install delve debugger (always for host arch, not target)
RUN --mount=type=cache,target=/go/pkg/mod \
    go install github.com/go-delve/delve/cmd/dlv@latest

# ============================================================================
# Stage: production
# Minimal runtime image with Alpine + git + ca-certificates
# Total size target: ~50MB (5MB Alpine + 35MB git + tools + binary)
# ============================================================================
FROM alpine:${ALPINE_VERSION} AS production

# Install only what's needed at runtime:
# - git: Required for cloning repos (pkg/git/repo.go)
# - ca-certificates: Required for HTTPS
RUN apk add --no-cache \
    git \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# Run as non-root user for security
# Create user before WORKDIR and COPY to avoid layer duplication
RUN addgroup -g 1000 kubechecks && \
    adduser -u 1000 -G kubechecks -s /bin/sh -D kubechecks

# Create app directory
WORKDIR /app

# Create volumes for policies and schemas
VOLUME /app/policies
VOLUME /app/schemas

# Copy stripped binary from builder with correct ownership
COPY --from=builder --chown=kubechecks:kubechecks /out/kubechecks /app/kubechecks

# Verify binary works
RUN /app/kubechecks help

USER kubechecks

CMD ["/app/kubechecks", "controller"]

# ============================================================================
# Stage: debug
# Debug image with delve for local development with Tilt
# Includes Go toolchain and source code for live_update hot reload
# ============================================================================
FROM golang:${GOLANG_VERSION}-alpine${ALPINE_VERSION} AS debug

# Install runtime dependencies and build tools
RUN apk add --no-cache \
    git \
    ca-certificates \
    && rm -rf /var/cache/apk/*

# Enable Go modules and disable CGO
ENV GO111MODULE=on
ENV CGO_ENABLED=0

# Create source directory for live_update
WORKDIR /src

# Copy go modules for caching (will be synced by Tilt live_update)
COPY go.mod go.sum ./
RUN go mod download

# Copy source code (will be synced by Tilt live_update)
COPY . .

# Copy debug binary and delve from debug-builder
COPY --from=debug-builder /out/kubechecks /app/kubechecks
COPY --from=debug-builder /go/bin/dlv /usr/local/bin/dlv

# Create volumes
WORKDIR /app
VOLUME /app/policies
VOLUME /app/schemas

# Expose delve debug port
EXPOSE 2345

# Run with delve for remote debugging
# Tilt live_update will rebuild /app/kubechecks when source changes
CMD ["/usr/local/bin/dlv", "--listen=:2345", "--api-version=2", "--headless=true", "--accept-multiclient", "exec", "--continue", "/app/kubechecks", "controller"]
