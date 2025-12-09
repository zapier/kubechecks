# Build System Documentation

Kubechecks uses Docker Buildx with BuildKit for building container images and native Go tooling for testing and validation.

## Quick Start

### Build locally
```bash
make build
```

### Run tests
```bash
make test
```

### Run full CI checks
```bash
make ci
```

## Common Commands

| Command | Description |
|---------|-------------|
| `make help` | Show all available targets |
| `make build` | Build production image for local platform |
| `make build-debug` | Build debug image with delve |
| `make test` | Run Go tests |
| `make lint` | Run golangci-lint |
| `make ci` | Run full CI pipeline (fmt, validate, lint, test) |
| `make rebuild-docs` | Rebuild documentation from code |
| `make clean` | Clean up build artifacts |

## Build Targets

### Local Development

```bash
# Build production image
make build

# Build debug image with delve
make build-debug

# Inspect built image
make inspect

# Run the built image
make run
```

### Multi-Architecture Builds

```bash
# Build for multiple architectures (amd64 + arm64)
make build-multiarch

# Build and push to registry
make push-multiarch IMAGE_REGISTRY=ghcr.io/zapier
```

### Testing & Validation

```bash
# Run Go tests
make test

# Run tests with coverage
make test-coverage

# Format code
make fmt

# Validate with go vet
make validate

# Run linter
make lint

# Rebuild documentation from code
make rebuild-docs

# Run all CI checks
make ci
```

## Configuration

### Environment Variables

- `IMAGE_NAME` - Docker image name (default: `kubechecks`)
- `IMAGE_TAG` - Docker image tag (default: git commit hash)
- `IMAGE_REGISTRY` - Container registry (default: `ghcr.io/zapier`)
- `PLATFORMS` - Build platforms (default: `linux/amd64,linux/arm64`)

### Examples

```bash
# Build with custom image name
make build IMAGE_NAME=my-kubechecks

# Build with custom tag
make build IMAGE_TAG=v1.0.0

# Push to custom registry
make push-multiarch IMAGE_REGISTRY=docker.io/myorg
```

## Version Management

Versions are automatically read from `.tool-versions`:
- `GOLANG_VERSION` - Go compiler version
- `GOLANGCI_LINT_VERSION` - golangci-lint version
- `ALPINE_VERSION` - Alpine base image version (3.21)

Git information is automatically extracted:
- `GIT_COMMIT` - Short commit hash
- `GIT_TAG` - Git tag or description
- `GIT_BRANCH` - Current branch

## Docker Buildx

The build system uses Docker Buildx with BuildKit for:
- **Multi-stage builds** - Separate stages for dependencies, building, and runtime
- **Cache mounts** - Fast rebuilds with Go module and build caching
- **Multi-platform** - Build for amd64 and arm64 simultaneously
- **Go cross-compilation** - Uses native Go cross-compilation instead of QEMU emulation for 8x faster ARM64 builds

### BuildKit Cache

For local development, BuildKit automatically caches:
- Go module downloads (`/go/pkg/mod`)
- Go build cache (`/root/.cache/go-build`)

For GitHub Actions:
- Uses GitHub Actions cache (`type=gha,mode=max`)
- Configured automatically when `GITHUB_ACTIONS` env var is set

## Image Details

### Production Image
- **Base**: Alpine 3.21 (~8MB)
- **Runtime deps**: git, ca-certificates (~13MB)
- **Binary**: Stripped Go binary (~133MB)
- **Total size**: ~161MB

### Debug Image
- Same as production + delve debugger
- Debug port: 2345
- Use with Tilt for local development

## Comparison: Earthly vs Buildx

| Feature | Earthly | Docker Buildx |
|---------|---------|---------------|
| Binary size | 223MB | 133MB (40% smaller) |
| Image size | 300MB | 161MB (46% smaller) |
| AMD64 build (CI) | ~6min | ~6min |
| ARM64 build (CI) | ~50min (QEMU) | ~6min (cross-compile, 8x faster) |
| Warm build (local) | ~3min | ~4sec (98% faster) |
| Tools included | helm, kustomize, git | git only |
| Multi-arch | ✅ | ✅ |
| Cache | Earthly cache | BuildKit cache |
| Cross-compilation | ❌ (uses QEMU) | ✅ (native Go) |

### Multi-Architecture Performance

**Key Optimization:** The Dockerfile uses Go's native cross-compilation instead of QEMU emulation for ARM64 builds.

- **Before (QEMU emulation):** ARM64 builds took ~50 minutes due to CPU instruction emulation
- **After (Go cross-compile):** ARM64 builds take ~6 minutes (same as AMD64)
- **How it works:** Sets `GOOS` and `GOARCH` environment variables, letting the Go compiler run natively on AMD64 while producing ARM64 binaries

This makes multi-arch builds in GitHub Actions **8x faster** without requiring expensive ARM64 runners.

## Troubleshooting

### Build fails with "no space left on device"
```bash
# Clean Docker build cache
make clean-cache

# Or prune all unused Docker data
docker system prune -a
```

### Slow builds
```bash
# Ensure BuildKit cache is working
docker buildx du

# Check BuildKit builder status
docker buildx inspect
```

### Multi-arch build fails
```bash
# Setup buildx builder
make setup-buildx

# Verify QEMU is installed (for ARM emulation)
docker run --rm --privileged multiarch/qemu-user-static --reset -p yes
```

## Development Setup

Install required tools:
```bash
make dev-setup
```

This installs:
- golangci-lint (for linting)
- Verifies Go and Docker installation

## CI/CD Integration

The Makefile is designed for use in CI/CD pipelines:

```yaml
# Example GitHub Actions usage
- name: Run tests
  run: make test

- name: Run CI checks
  run: make ci

- name: Rebuild docs
  run: make rebuild-docs

- name: Build and push
  run: make push-multiarch
  env:
    IMAGE_REGISTRY: ghcr.io/${{ github.repository_owner }}
```

## Migration from Earthly

If you're migrating from Earthly:

| Earthly Target | Makefile Target | Notes |
|----------------|-----------------|-------|
| `earthly +docker` | `make build` | Single platform |
| `earthly +docker-multiarch` | `make build-multiarch` | Multi-arch |
| `earthly +docker-debug` | `make build-debug` | Debug image |
| `earthly +test-golang` | `make test` | Go tests |
| `earthly +test-helm` | `make test-helm` | Helm chart tests |
| `earthly +golang-ci-lint` | `make lint` | Linting |
| `earthly +fmt-golang` | `make fmt` | Formatting |
| `earthly +validate-golang` | `make validate` | Validation |
| `earthly +rebuild-docs` | `make rebuild-docs` | Documentation |
| `earthly +ci-golang` | `make ci` | Full CI |
| `earthly +ci-helm` | `make test-helm` | Helm CI |

## Additional Resources

- [Docker Buildx Documentation](https://docs.docker.com/buildx/working-with-buildx/)
- [BuildKit Documentation](https://github.com/moby/buildkit)
- [Multi-platform Images](https://docs.docker.com/build/building/multi-platform/)
