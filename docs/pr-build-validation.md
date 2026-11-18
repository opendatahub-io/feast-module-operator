# PR build validation

`.github/workflows/pr-build-validation.yml` simulates the Konflux pull-request
build defined by `.tekton/odh-feast-module-operator-pull-request.yaml` and checks
the contracts that a `go build` cannot see.

Run the same checks locally with `scripts/validate-build.sh`.

```bash
./scripts/validate-build.sh static      # PipelineRun inputs, Containerfile, manifest path contract
./scripts/validate-build.sh hermetic    # go.mod builds with GOPROXY=off
./scripts/validate-build.sh manifests   # kustomize entry points, schemas, RBAC subjects
./scripts/validate-build.sh chart       # helm render + regenerate-and-diff against `make helm`
./scripts/validate-build.sh image       # build the image, run it, assert the manifests ship
./scripts/validate-build.sh all
./scripts/validate-build.sh target      # dockerfile + build context from the PipelineRun
./scripts/validate-build.sh builder-go  # Go version the Containerfile's builder pins
```

The dockerfile and build context always come from the PipelineRun params, so a
PR that points Konflux at a different containerfile cannot pass here by
validating `Containerfile` instead.

## What each job covers

| Job | Catches |
|-----|---------|
| Static Validation Checks | A PipelineRun that points at a missing Containerfile or path, a `COPY` whose source is not in the build context, an image that stops shipping `config/manifests/`, a chart and image that disagree about where manifests live, duplicate env var names |
| Hermetic Build Preflight | A module set that only resolves with the network up, a `go.mod` that needs a newer toolchain than the builder image provides |
| Validate Manifests | A kustomize entry point that no longer builds, a built-in `kind`/`apiVersion` typo, an RBAC binding that names a ServiceAccount the manifests never create |
| Validate Helm Chart | A chart that renders the wrong resources, and `config/chart` drifting from `make helm` |
| Build Container Image | A Containerfile that builds a binary the runtime image cannot start, or an image that ships no `manifests/feastoperator/` |

## Why the chart check exists

`config/chart` is generated (`make helm`) from `config/default` and is the path
`opendatahub-operator` consumes through `manifests-config.yaml`. The repository
CI regenerates CRDs and deepcopy code, but nothing regenerates or verifies the
chart, so a kustomize change can ship a chart that does not contain it. That is
the failure class behind RHOAIENG-96436 (odh-feast-operator manifests out of
sync, an RC blocker).

The check fails when a pull request introduces or leaves drift. Because `main`
itself can be behind on regeneration, the base ref is regenerated as well and
the two sets of drifted files are compared:

| Situation | Result |
| --- | --- |
| `config/chart` matches `make helm` | pass |
| Drift exists, and the base ref drifts the same files | warning, pass |
| Drift exists and the base ref does not drift the same files | fail |
| No base ref supplied (for example a local run) | fail |

The middle row is the only tolerated case, and it cannot hide a new problem: a
kustomize change that is not regenerated produces a different set of drifted
files from the base ref, so it fails. The warning disappears on its own once the
committed chart is regenerated on the base branch.

## Why the RBAC subject check exists

A kustomize `namePrefix` rewrites the ServiceAccount name but not the subjects
of a RoleBinding or ClusterRoleBinding. The operator then starts with an
identity that has no permissions and crash-loops. That is RHOAIENG-100514 and
RHOAIENG-100515 (Data module NotReady, DSC never Ready) and RHOAIENG-99631
(DSC never Ready because rhods-operator renders the feast chart).

## What this does not cover

- **Cross-repository drift.** The image here is consumed by
  `opendatahub-operator` through `manifests-config.yaml`. A change in this
  repository that requires a consumer update is not visible from here.
- **Downstream fork sync.** The `red-hat-data-services` fork is synced
  separately; this workflow only compares the chart against the sources in this
  repository.
- **Reconcile behaviour.** RHOAIENG-101993 (the reconciler crashing with
  `is not a WithReleases` after an `odh-platform-utilities` bump) is only
  reachable by running the operator. `make test-e2e` and `make test-integration`
  exist but no workflow runs them.
- **Multi-architecture images.** The PipelineRun does not set
  `build-platforms`, so the shared pipeline produces `linux/x86_64` only.
  RHOAIENG-79767 was an `ImagePullBackOff` on ppc64le for exactly this reason.
  The static job warns instead of failing because the fix is a PipelineRun
  change, not something a PR can infer.

## Skipping

Add `[skip konflux-sim]` to the PR title, or the `skip-konflux-sim` label, to
skip the run. The summary job reports a skip explicitly; it never reports
success for checks that did not run.
