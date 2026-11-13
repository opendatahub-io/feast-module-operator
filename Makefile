# Include local overrides if present (gitignored).
-include local.mk

# Image URL to use all building/pushing image targets
IMG ?= ttl.sh/opendatahub-feast-operator-$(shell git rev-parse --short HEAD 2>/dev/null || echo dev):1h
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

## Tool Versions
CONTROLLER_GEN_VERSION ?= v0.20.1
KUSTOMIZE_VERSION      ?= v5.8.1
GOLANGCI_LINT_VERSION  ?= v2.11.4
HELM_VERSION           ?= v4.2.0

## Tool Commands
CONTROLLER_GEN = go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
KUSTOMIZE      = go run sigs.k8s.io/kustomize/kustomize/v5@$(KUSTOMIZE_VERSION)
GOLANGCI_LINT  = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
HELM           = go run helm.sh/helm/v4/cmd/helm@$(HELM_VERSION)

KUBECTL ?= kubectl

MODULE_NAME ?= $(notdir $(CURDIR))

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=opendatahub-feast-manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: fmt
fmt: ## Run golangci-lint formatters against code.
	$(GOLANGCI_LINT) fmt

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: deps
deps: ## Tidy and verify Go module dependencies.
	go mod tidy
	go mod verify

.PHONY: test
test: manifests generate fmt vet ## Run unit tests.
	go test -ldflags "$(LDFLAGS)" $$(go list ./... | grep -v /e2e | grep -v /integration) -coverprofile cover.out

.PHONY: test-integration-run
test-integration-run: ## Run integration tests only (cluster must be prepared).
	INTEGRATION_TEST_NAMESPACE="$(INTEGRATION_TEST_NAMESPACE)" go test -count=1 -ldflags "$(LDFLAGS)" ./test/integration/ -tags=integration -v -timeout 5m -failfast

.PHONY: test-integration-setup
test-integration-setup: manifests generate ## Prepare the cluster for integration tests.
	$(MAKE) cleanup-integration
	$(MAKE) install

.PHONY: test-integration
test-integration: test-integration-setup test-integration-run ## Run integration tests (prepare cluster, then run tests). ODH_PLATFORM_TYPE=odh|rhoai|managed-rhoai

.PHONY: test-e2e-run
test-e2e-run: ## Run e2e tests only (operator must already be deployed).
	OPERATOR_NAMESPACE="$(OPERATOR_NAMESPACE)" go test -count=1 -ldflags "$(LDFLAGS)" ./test/e2e/ -tags=e2e -v -timeout 5m -failfast

.PHONY: test-e2e-setup
test-e2e-setup: ## Prepare the cluster for e2e tests.
	@:

.PHONY: test-e2e
test-e2e: test-e2e-cleanup test-e2e-setup deploy-helm test-e2e-run ## Run e2e tests (clean, prepare, deploy, then run). ODH_PLATFORM_TYPE=odh|rhoai|managed-rhoai
.PHONY: test-integration-cleanup
test-integration-cleanup: ## Clean up integration test resources from the cluster.
	./hack/scripts/cleanup-integration.sh "$(INTEGRATION_TEST_NAMESPACE)"

.PHONY: cleanup-integration
cleanup-integration: test-integration-cleanup ## Alias for test-integration-cleanup.

.PHONY: test-e2e-cleanup
test-e2e-cleanup: ## Clean up e2e test resources and uninstall operator from the cluster.
	./hack/scripts/cleanup-e2e.sh "$(OPERATOR_NAMESPACE)" "$(HELM_RELEASE)"

.PHONY: cleanup-e2e
cleanup-e2e: test-e2e-cleanup ## Alias for test-e2e-cleanup.

.PHONY: test-e2e-upgrade-pre
test-e2e-upgrade-pre: ## Run pre-upgrade smoke tests (operator must be deployed).
	go test ./test/e2e/upgrade/pre/ -v -tags=e2e -timeout 10m

.PHONY: test-e2e-upgrade-post
test-e2e-upgrade-post: ## Run post-upgrade smoke tests (operator must be upgraded).
	go test ./test/e2e/upgrade/post/ -v -tags=e2e -timeout 10m

.PHONY: lint
lint: ## Run golangci-lint linter.
	$(GOLANGCI_LINT) run --build-tags integration,e2e,upgrade

.PHONY: lint-fix
lint-fix: ## Run golangci-lint linter and perform fixes.
	$(GOLANGCI_LINT) run --fix

.PHONY: get-manifests
get-manifests: ## Download component manifests.
	./hack/scripts/get-manifests.sh

.PHONY: verify-rbac
verify-rbac: manifests ## Verify module operator RBAC covers all operand permissions and chart is in sync.
	@go run ./hack/verify-rbac config/rbac/role.yaml config/manifests/feastoperator/rbac/role.yaml \
		config/chart/templates/rbac.authorization.k8s.io_v1_clusterrole.yaml opendatahub-feast-manager-role

##@ Build

VERSION_PKG    = github.com/opendatahub-io/feast-module-operator/pkg/version
VERSION       ?= 0.0.0-dev
GIT_COMMIT    ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GIT_BRANCH    ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
GIT_REPO      ?= $(shell git remote get-url origin 2>/dev/null || echo unknown)
GOOS          ?= $(shell go env GOOS)
GOARCH        ?= $(shell go env GOARCH)
CGO_ENABLED   ?= 0
BIN_DIR       ?= bin
BIN_NAME      ?= manager
LDFLAGS        = -X $(VERSION_PKG).Version=$(VERSION) \
                 -X $(VERSION_PKG).Commit=$(GIT_COMMIT) \
                 -X $(VERSION_PKG).Branch=$(GIT_BRANCH) \
                 -X $(VERSION_PKG).Repo=$(GIT_REPO)

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BIN_NAME)" cmd/main.go

.PHONY: build-bin
build-bin: ## Build manager binary only (for Containerfile; run container-prep on host first).
	mkdir -p "$(BIN_DIR)"
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "$(LDFLAGS)" -o "$(BIN_DIR)/$(BIN_NAME)" cmd/main.go

.PHONY: container-prep
container-prep: manifests generate ## On host: regenerate code and fetch manifests before container-build.
	@if [ "$(SKIP_GET_MANIFESTS)" = "1" ] || [ -f config/manifests/feastoperator/manager/manager.yaml ]; then \
		echo "Skipping get-manifests (bundled manifests present or SKIP_GET_MANIFESTS=1)"; \
	else \
		$(MAKE) get-manifests; \
	fi

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	ODH_MODULE_OPERATOR_MANIFESTS_PATH=config/manifests go run -ldflags "$(LDFLAGS)" ./cmd/main.go operator

.PHONY: docker-build
docker-build: container-prep ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -f Containerfile --build-arg LDFLAGS="$(LDFLAGS)" -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

.PHONY: container-build
container-build: docker-build ## Alias for docker-build.

.PHONY: container-push
container-push: docker-push ## Alias for docker-push.

##@ Helm

HELM_NAMESPACE ?= opendatahub-feast-system
HELM_RELEASE   ?= opendatahub-feast-operator
OPERATOR_NAMESPACE ?= $(HELM_NAMESPACE)
INTEGRATION_TEST_NAMESPACE ?= integration-test
HELM_EXTRA_ARGS ?=

# Friendly platform type names, mapped to the common.Platform values the
# operator compares against. Selects the manifests overlay (ODH vs RHOAI)
# for both integration tests (env var) and e2e/deploys (chart extraEnvs),
# and which feast repo get-manifests fetches. The source-commit marker always
# records both ODH and RHOAI pins so sync does not diverge on that hash file.
ODH_PLATFORM_TYPE ?= # odh | rhoai | managed-rhoai

export ODH_PLATFORM_TYPE

ifneq ($(ODH_PLATFORM_TYPE),)
ifeq ($(ODH_PLATFORM_TYPE),odh)
ODH_PLATFORM_TYPE_VALUE := OpenDataHub
else ifeq ($(ODH_PLATFORM_TYPE),rhoai)
ODH_PLATFORM_TYPE_VALUE := OpenShift AI Self-Managed
else ifeq ($(ODH_PLATFORM_TYPE),managed-rhoai)
ODH_PLATFORM_TYPE_VALUE := OpenShift AI Cloud Service
else
$(error invalid ODH_PLATFORM_TYPE '$(ODH_PLATFORM_TYPE)': expected odh, rhoai or managed-rhoai)
endif
endif

ifneq ($(ODH_PLATFORM_TYPE_VALUE),)
ODH_PLATFORM_TYPE_HELM_ARGS := --set-string 'extraEnvs.ODH_MODULE_OPERATOR_PLATFORM_TYPE=$(ODH_PLATFORM_TYPE_VALUE)'
export ODH_MODULE_OPERATOR_PLATFORM_TYPE := $(ODH_PLATFORM_TYPE_VALUE)
endif

.PHONY: helm
helm: manifests generate ## Generate Helm chart from kustomize output.
	rm -rf config/chart
	$(KUSTOMIZE) build config/default | go run ./cmd/main.go chartgen --output config/chart

.PHONY: helm-status
helm-status: ## Show Helm release status.
	$(HELM) status $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: deploy-helm
deploy-helm: ## Deploy controller via Helm chart.
	@resolved_img="$$(bash ./hack/scripts/resolve-image-ref.sh "$(IMG)")"; \
		if [ -z "$$resolved_img" ]; then \
			echo "ERROR: empty image reference (IMG='$(IMG)')" >&2; exit 1; \
		fi; \
		if [ -f config/chart/templates/apiextensions.k8s.io_v1_customresourcedefinition.yaml ]; then \
			echo "ERROR: stale chart has a CRD in templates/; run 'make helm' before deploying." >&2; exit 1; \
		fi; \
		helm_extra="$(HELM_EXTRA_ARGS)"; \
		if $(KUBECTL) get crd feastoperators.components.platform.opendatahub.io >/dev/null 2>&1; then \
			echo "Protecting the existing FeastOperator CRD during Helm template-to-crds/ migration"; \
			$(KUBECTL) annotate crd feastoperators.components.platform.opendatahub.io \
				helm.sh/resource-policy=keep --overwrite || { \
					echo "ERROR: could not protect the CRD; refusing to upgrade." >&2; exit 1; \
				}; \
		fi; \
		if [ "$(SKIP_HELM_CRDS)" = "1" ] || $(KUBECTL) get crd feastoperators.components.platform.opendatahub.io >/dev/null 2>&1; then \
			echo "Skipping Helm CRD install (CRD already on cluster or SKIP_HELM_CRDS=1)"; \
			echo "Applying CRD update from config/crd/bases/ ..."; \
			$(KUBECTL) apply --server-side --force-conflicts -f config/crd/bases/ || { \
				echo "ERROR: CRD update failed; refusing to deploy. Run 'make install' first." >&2; exit 1; \
			}; \
			helm_extra="$$helm_extra --skip-crds"; \
		fi; \
		echo "Deploying image: $$resolved_img"; \
		$(HELM) upgrade --install $(HELM_RELEASE) config/chart \
			--namespace $(HELM_NAMESPACE) --create-namespace \
			--set-string namespace="$(HELM_NAMESPACE)" \
			--set-string image.fullRef="$$resolved_img" \
			--wait --timeout 5m $$helm_extra $(ODH_PLATFORM_TYPE_HELM_ARGS)

.PHONY: push-openshift-image
push-openshift-image: ## Push a built image to the OpenShift internal registry and print the pullspec.
	@bash ./hack/scripts/push-openshift-image.sh "$(IMG)" "$(HELM_NAMESPACE)" "$(MODULE_NAME)"

.PHONY: deploy-openshift
deploy-openshift: ## Build locally, push to OpenShift internal registry, and deploy via Helm.
	$(MAKE) container-build
	@img="$$(bash ./hack/scripts/push-openshift-image.sh "$(IMG)" "$(HELM_NAMESPACE)" "$(MODULE_NAME)")"; \
		if [ -z "$$img" ]; then \
			echo "ERROR: image push failed — empty pullspec." >&2; \
			echo "  Registry route missing? Try: make deploy-external-img IMG=<pushed-image>" >&2; \
			exit 1; \
		fi; \
		echo "Using image: $$img"; \
		$(MAKE) helm; \
		$(MAKE) deploy-helm IMG="$$img"

.PHONY: deploy-external-img
deploy-external-img: helm ## Deploy IMG as-is (skip OpenShift internal registry push).
	@test -n "$(IMG)" || (echo "ERROR: set IMG to an image the cluster can pull (e.g. quay.io/... or ttl.sh/...)" >&2; exit 1)
	@echo "Deploying external image: $(IMG)"
	$(MAKE) deploy-helm IMG="$(IMG)"

.PHONY: undeploy-helm
undeploy-helm: ## Undeploy controller installed via Helm.
	-$(HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: install
install: manifests ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	@out="$$( $(KUSTOMIZE) build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

##@ Helpers

.PHONY: kustomize
kustomize:

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
