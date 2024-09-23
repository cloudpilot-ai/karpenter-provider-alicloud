CLUSTER_NAME ?= $(shell kubectl config view --minify -o jsonpath='{.clusters[].name}' | rev | cut -d"/" -f1 | rev | cut -d"." -f1)

## Inject the app version into operator.Version
LDFLAGS ?= -ldflags=-X=sigs.k8s.io/karpenter/pkg/operator.Version=$(shell git describe --tags --always | cut -d"v" -f2)

GOFLAGS ?= $(LDFLAGS)
WITH_GOFLAGS = GOFLAGS="$(GOFLAGS)"

## Extra helm options
CLUSTER_ENDPOINT ?= $(shell kubectl config view --minify -o jsonpath='{.clusters[].cluster.server}')
# CR for local builds of Karpenter
KARPENTER_NAMESPACE ?= kube-system
KARPENTER_VERSION ?= $(shell git tag --sort=committerdate | tail -1 | cut -d"v" -f2)
# KO_DOCKER_REPO ?= ${AWS_ACCOUNT_ID}.dkr.ecr.${AWS_DEFAULT_REGION}.amazonaws.com/dev
KO_DOCKER_REPO ?= ko.local
KOCACHE ?= ~/.ko

# Common Directories
MOD_DIRS = $(shell find . -path "./website" -prune -o -name go.mod -type f -print | xargs dirname)
KARPENTER_CORE_DIR = $(shell go list -m -f '{{ .Dir }}' sigs.k8s.io/karpenter)

# TEST_SUITE enables you to select a specific test suite directory to run "make e2etests" against
TEST_SUITE ?= "..."

help: ## Display help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

presubmit: verify test ## Run all steps in the developer loop

ci-test: test coverage ## Runs tests and submits coverage

ci-non-test: verify licenses vulncheck ## Runs checks other than tests

run: ## Run Karpenter controller binary against your local cluster
	SYSTEM_NAMESPACE=${KARPENTER_NAMESPACE} \
		KUBERNETES_MIN_VERSION="1.19.0-0" \
		DISABLE_LEADER_ELECTION=true \
		CLUSTER_NAME=${CLUSTER_NAME} \
		INTERRUPTION_QUEUE=${CLUSTER_NAME} \
		FEATURE_GATES="SpotToSpotConsolidation=true" \
		go run ./cmd/controller/main.go

test: ## Run tests
	go test ./pkg/... \
		-cover -coverprofile=coverage.out -outputdir=. -coverpkg=./... \
		--ginkgo.focus="${FOCUS}" \
		--ginkgo.randomize-all \
		--ginkgo.vv

benchmark:
	go test -tags=test_performance -run=NoTests -bench=. ./...

coverage:
	go tool cover -html coverage.out -o coverage.html

verify: tidy download ## Verify code. Includes dependencies, linting, formatting, etc
	go generate ./...
	hack/boilerplate.sh

vulncheck: ## Verify code vulnerabilities
	@govulncheck ./pkg/...

image: ## Build the Karpenter controller images using ko build
	$(eval CONTROLLER_IMG=$(shell $(WITH_GOFLAGS) KOCACHE=$(KOCACHE) KO_DOCKER_REPO="$(KO_DOCKER_REPO)" ko build --bare github.com/cloudpilot-ai/karpenter-provider-alicloud/cmd/controller))
	$(eval IMG_REPOSITORY=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 1 | cut -d ":" -f 1))
	$(eval IMG_TAG=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 1 | cut -d ":" -f 2 -s))
	$(eval IMG_DIGEST=$(shell echo $(CONTROLLER_IMG) | cut -d "@" -f 2))

apply: verify image ## Deploy the controller from the current state of your git repository into your ~/.kube/config cluster
	kubectl apply -f ./pkg/apis/crds/
	helm upgrade --install karpenter charts/karpenter --namespace ${KARPENTER_NAMESPACE} \
        $(HELM_OPTS) \
        --set logLevel=debug \
        --set controller.image.repository=$(IMG_REPOSITORY) \
        --set controller.image.tag=$(IMG_TAG) \
        --set controller.image.digest=$(IMG_DIGEST)

install:  ## Deploy the latest released version into your ~/.kube/config cluster
	@echo Upgrading to ${KARPENTER_VERSION}
	helm upgrade --install karpenter oci://public.ecr.aws/karpenter/karpenter --version ${KARPENTER_VERSION} --namespace ${KARPENTER_NAMESPACE} \
		$(HELM_OPTS)

delete: ## Delete the controller from your ~/.kube/config cluster
	helm uninstall karpenter --namespace ${KARPENTER_NAMESPACE}

toolchain: ## Install developer toolchain
	./hack/toolchain.sh

tidy: ## Recursively "go mod tidy" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod tidy $(newline))

download: ## Recursively "go mod download" on all directories where go.mod exists
	$(foreach dir,$(MOD_DIRS),cd $(dir) && go mod download $(newline))

update-karpenter: ## Update kubernetes-sigs/karpenter to latest
	go get -u sigs.k8s.io/karpenter@HEAD
	go mod tidy

.PHONY: help presubmit ci-test ci-non-test run test benchmark coverage verify vulncheck image apply install delete toolchain tidy download update-karpenter

define newline


endef
