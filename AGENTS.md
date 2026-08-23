<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-04-06 | Updated: 2026-04-06 -->

# listener-operator

## Purpose
Manages Kubernetes service discovery and network listener lifecycle for the Kubedoop operator ecosystem. Provides `ListenerClass`, `Listener`, and `PodListeners` CRDs to abstract service exposure (NodePort, LoadBalancer, ClusterIP) and includes a CSI driver plugin for mounting listener address information into pods.

## Key Files
| File | Description |
|------|-------------|
| `go.mod` | Go module dependencies (`github.com/zncdatadev/listener-operator`) |
| `Makefile` | Build, generate, lint, test, and deploy commands |
| `PROJECT` | Kubebuilder project metadata (domain: `kubedoop.dev`) |
| `build/` | Dockerfiles for operator and CSI plugin images |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `api/v1alpha1/` | CRD type definitions: `ListenerClass`, `Listener`, `PodListeners` |
| `cmd/` | Operator entry point (`main.go`) and CSI plugin entry point (`csiplugin/`) |
| `config/` | Kubernetes manifests and kustomize configs (CRDs, RBAC, manager) |
| `internal/controller/` | Reconcilers for `ListenerClass`, `Listener`, `PodListeners`, and `listener/` sub-package |
| `internal/csi/` | CSI driver implementation (identity, node, controller, driver) |
| `internal/util/` | Shared utilities |
| `pkg/` | Exported packages |
| `deploy/` | Deployment manifests |
| `examples/` | Example CRD manifests |
| `test/` | E2E test suites |

## For AI Agents

### Working In This Directory
- Standard Kubebuilder operator structure with an additional CSI plugin component
- Uses `operator-go` framework (`github.com/zncdatadev/operator-go`) for reconciliation
- Two binaries: the operator (`cmd/main.go`) and the CSI node plugin (`cmd/csiplugin/`)
- Run `make generate` after modifying API types to regenerate deepcopy functions
- Run `make manifests` after modifying API types to regenerate CRDs and RBAC
- Run `make test` for unit tests
- Run `make lint` to run golangci-lint

### Testing Requirements
- Unit/integration tests in `internal/controller/` using `envtest` (suite_test.go)
- E2E tests in `test/e2e/`
- Requires a Kubernetes cluster for E2E testing

### Common Patterns
- Controllers in `internal/controller/` follow the `operator-go` GenericReconciler pattern
- CRDs use `v1alpha1` API version under group `listeners.kubedoop.dev`
- CSI driver in `internal/csi/` implements the Container Storage Interface for listener address injection
- The CSI node plugin mounts listener endpoints (IP/hostname/port) into pod volumes

## Dependencies

### Internal
- `../operator-go` - Shared operator framework (`github.com/zncdatadev/operator-go v0.12.6`)

### External
- `sigs.k8s.io/controller-runtime` - Kubernetes controller runtime
- `k8s.io/client-go` - Kubernetes client
- `github.com/container-storage-interface/spec` - CSI specification
- `google.golang.org/grpc` - gRPC for CSI communication
- Kubernetes 1.26+
- Go 1.23+

### AI Worktree Development Mode

**IMPORTANT**: When making code changes, work in a worktree under `.worktree/`, NOT in the main working directory.

#### Workflow
1. Create worktree: `git worktree add .worktree/<branch-name> -b <branch-name>`
2. Work in `.worktree/<branch-name>/` directory
3. Test: `cd .worktree/<branch-name> && make lint && make test`
4. Commit changes in the worktree
5. Push and create PR from the worktree branch
6. Cleanup: `git worktree remove .worktree/<branch-name>`

#### Rules
- NEVER modify files directly in the main working directory
- Each task gets its own worktree with a descriptive branch name
- Run `make generate` if API structs are modified
- Run `make lint && make test` before committing

<!-- MANUAL: Any manually added notes below this line are preserved on regeneration -->
