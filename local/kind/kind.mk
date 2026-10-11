ifndef KIND_MK_INCLUDED
KIND_MK_INCLUDED := 1

# Local Kubernetes testing with kind.
# Keep kind-help as the first target for standalone use.

KIND_DIR          := local/kind
KIND_CLUSTER      ?= eks-v2-local
KIND_CONTEXT      := kind-$(KIND_CLUSTER)

KIND_NODE_IMAGE   ?= kindest/node:v1.35.8

K8S_DIR           := kubernetes
CHART             := $(K8S_DIR)/charts/microservice
SERVICES_DIR      := $(K8S_DIR)/deploy/services
ENVS_DIR          := $(K8S_DIR)/deploy/envs
PLATFORM_DIR      := $(K8S_DIR)/platform


KIND_PLATFORM     ?= postgres redis
KIND_PLATFORM_SKIP ?= snapshot-schedule.yaml

PG_SECRET         ?= postgres-credentials
PG_USER_KEY       ?= username
PG_PASS_KEY       ?= password
PG_LOCAL_USER     ?= app
PG_LOCAL_PASS     ?= localdev

APPS_DIR          := $(PLATFORM_DIR)/apps
HELM_VALUES_DIR   := $(K8S_DIR)/helm-values
argo_version       = $(shell helm template kind-apps $(APPS_DIR) 2>/dev/null | \
                       awk -v c=$(1) '$$1=="chart:" && $$2==c {f=1; next} f && $$1=="targetRevision:" {print $$2; exit}')
METRICS_SERVER_REPO    := https://kubernetes-sigs.github.io/metrics-server/
METRICS_SERVER_VERSION ?= $(call argo_version,metrics-server)

TRAEFIK_REPO      := https://traefik.github.io/charts
TRAEFIK_VERSION   ?= $(call argo_version,traefik)

# Argo Rollouts: same chart version as the ArgoCD app, so kind matches the cloud envs.
ARGO_ROLLOUTS_REPO    := https://argoproj.github.io/argo-helm
ARGO_ROLLOUTS_VERSION ?= $(call argo_version,argo-rollouts)

FLOCI_INIT        ?= scripts/floci-init.sh
VALUES_KIND       := $(KIND_DIR)/values-kind.yaml

ENV               ?= dev
NAMESPACE         ?= order-platform

SERVICES          ?= $(notdir $(patsubst %/,%,$(wildcard $(SERVICES_DIR)/*/)))

APP_SRC_DIR       ?= services
LOCAL_REPO        := order-platform-local
local_image        = $(LOCAL_REPO):$(1)-local

# Value files for one service ($(1)) in one env ($(2)), in ApplicationSet order,
# with the kind overlay applied last.
_helm-values = \
  -f $(SERVICES_DIR)/$(1)/values.yaml \
  -f $(ENVS_DIR)/$(2)/common.yaml \
  -f $(ENVS_DIR)/$(2)/$(1).yaml \
  -f $(VALUES_KIND) \
  --set image.tag=$(1)-local

# Kubernetes resources that must not appear in kind-rendered manifests.
# AnalysisTemplate: kind has no Prometheus, so canary analysis must stay off (analysis.enabled: false).
KIND_FORBIDDEN := ExternalSecret|Certificate|ServiceMonitor|AnalysisTemplate

.PHONY: kind-help kind-check kind-up kind-down kind-clean kind-load kind-platform \
        kind-render kind-build kind-deploy kind-status kind-all _kind-guard _kind-env

kind-help: ## Show kind targets
	@grep -hE '^kind-[a-zA-Z_-]+:.*## ' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "  ENV=$(ENV)  NAMESPACE=$(NAMESPACE)  KIND_CLUSTER=$(KIND_CLUSTER)"
	@echo "  SERVICES=$(SERVICES)"

kind-check: ## Check required tools are installed
	@for t in docker kind kubectl helm; do \
	  command -v $$t >/dev/null || { echo "missing: $$t"; exit 1; }; \
	done
	@docker info >/dev/null 2>&1 || { echo "Docker is not running"; exit 1; }
	@echo "All tools present"

kind-up: kind-check ## Create the local cluster if it does not exist
	@if kind get clusters | grep -qx '$(KIND_CLUSTER)'; then \
	  echo "Cluster $(KIND_CLUSTER) already exists"; \
	else \
	  kind create cluster --name $(KIND_CLUSTER) \
	    --config $(KIND_DIR)/kind-config.yaml \
	    --image $(KIND_NODE_IMAGE) --wait 120s; \
	fi
	kubectl config use-context $(KIND_CONTEXT)
	kubectl get nodes

kind-down: ## Delete the local cluster
	kind delete cluster --name $(KIND_CLUSTER)

kind-clean: kind-down ## Delete the cluster and locally built service images
	@imgs=$$(docker images $(LOCAL_REPO) -q | sort -u); \
	if [ -n "$$imgs" ]; then docker rmi -f $$imgs; else echo "No $(LOCAL_REPO) images to remove"; fi

# Prevent commands from running against the wrong cluster.
_kind-guard:
	@ctx=$$(kubectl config current-context); \
	if [ "$$ctx" != "$(KIND_CONTEXT)" ]; then \
	  echo "Refusing: current context is '$$ctx', expected '$(KIND_CONTEXT)'"; exit 1; \
	fi

# Ensure the selected environment has a values directory.
_kind-env:
	@test -d $(ENVS_DIR)/$(ENV) || { \
	  echo "No $(ENVS_DIR)/$(ENV)/ - available: $$(ls $(ENVS_DIR))"; exit 1; }

kind-build: ## Build images for SERVICES from services/<svc>
	@set -e; $(foreach svc,$(SERVICES), \
	  echo "==> building $(svc)"; \
	  docker build -t $(call local_image,$(svc)) $(APP_SRC_DIR)/$(svc);)

kind-load: _kind-guard ## Load built images into the cluster
	kind load docker-image --name $(KIND_CLUSTER) \
	  $(foreach svc,$(SERVICES),$(call local_image,$(svc)))

kind-platform: _kind-guard ## Install local platform dependencies
	@test -n "$(TRAEFIK_VERSION)" || { echo "could not read Traefik version from $(APPS_DIR)"; exit 1; }
	@test -n "$(METRICS_SERVER_VERSION)" || { echo "could not read metrics-server version from $(APPS_DIR)"; exit 1; }
	@test -n "$(ARGO_ROLLOUTS_VERSION)" || { echo "could not read Argo Rollouts version from $(APPS_DIR)"; exit 1; }
	@test -f $(FLOCI_INIT) || { echo "missing $(FLOCI_INIT) (floci init script)"; exit 1; }
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
# Apply kind-specific stand-ins for AWS-managed resources.
	kubectl apply -f $(KIND_DIR)/storageclass-gp3.yaml
	kubectl create secret generic $(PG_SECRET) -n $(NAMESPACE) \
	  --from-literal=$(PG_USER_KEY)=$(PG_LOCAL_USER) \
	  --from-literal=$(PG_PASS_KEY)=$(PG_LOCAL_PASS) \
	  --dry-run=client -o yaml | kubectl apply -f -
	kubectl create configmap floci-init -n $(NAMESPACE) \
	  --from-file=init.sh=$(FLOCI_INIT) --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -n $(NAMESPACE) -f $(KIND_DIR)/floci/
	helm upgrade --install metrics-server metrics-server \
	  --repo $(METRICS_SERVER_REPO) --version $(METRICS_SERVER_VERSION) \
	  --namespace kube-system -f $(KIND_DIR)/metrics-server-values.yaml
	helm upgrade --install traefik traefik \
	  --repo $(TRAEFIK_REPO) --version $(TRAEFIK_VERSION) \
	  --namespace traefik --create-namespace \
	  -f $(HELM_VALUES_DIR)/traefik-values.yaml -f $(KIND_DIR)/traefik-values.yaml
# Argo Rollouts controller + CRDs: the services render as Rollouts, so this must exist before kind-deploy.
	helm upgrade --install argo-rollouts argo-rollouts \
	  --repo $(ARGO_ROLLOUTS_REPO) --version $(ARGO_ROLLOUTS_VERSION) \
	  --namespace argo-rollouts --create-namespace --wait
	@set -e; for app in $(KIND_PLATFORM); do \
	  dir=$(PLATFORM_DIR)/$$app; \
	  echo "==> $$app"; \
	  for f in $$(find $$dir -name '*.yaml' | sort); do \
	    case " $(KIND_PLATFORM_SKIP) " in \
	      *" $$(basename $$f) "*) echo "    skip $$f";; \
	      *) kubectl apply -n $(NAMESPACE) -f $$f;; \
	    esac; \
	  done; \
	done
	@echo "==> waiting for platform pods"
	kubectl rollout status deployment/metrics-server -n kube-system --timeout=180s
	@set -e; for ns in traefik; do \
	  for r in $$(kubectl get deployment -n $$ns -o name); do \
	    kubectl rollout status $$r -n $$ns --timeout=180s; \
	  done; \
	done
	@set -e; for kind in deployment statefulset; do \
	  for r in $$(kubectl get $$kind -n $(NAMESPACE) -o name); do \
	    kubectl rollout status $$r -n $(NAMESPACE) --timeout=180s; \
	  done; \
	done

kind-render: ## Render all services for every environment and check kind compatibility
	@set -e; for env in $$(ls $(ENVS_DIR)); do \
	  for svc in $(SERVICES); do \
	    printf '  %-8s %-22s ' $$env $$svc; \
	    out=$$(helm template $$svc $(CHART) $(call _helm-values,$$svc,$$env)); \
	    found=$$(echo "$$out" | grep -oE '^kind: ($(KIND_FORBIDDEN))$$' | sort -u | tr '\n' ' '); \
	    if [ -n "$$found" ]; then echo "FAIL: renders $$found"; exit 1; fi; \
	    if echo "$$out" | grep -q 'dkr.ecr'; then echo "FAIL: still points at ECR"; exit 1; fi; \
	    echo ok; \
	  done; \
	done
	@echo "All services render for kind"

kind-deploy: _kind-guard _kind-env  ## Deploy SERVICES for one ENV at a time (ENV=dev|staging|prod)
	@set -e; $(foreach svc,$(SERVICES), \
	  echo "==> $(svc) ($(ENV))"; \
	  helm upgrade --install $(svc) $(CHART) \
	    --namespace $(NAMESPACE) --create-namespace \
	    $(call _helm-values,$(svc),$(ENV)) \
	    --wait --timeout 5m;)
# helm --wait does not track Argo Rollouts, so wait for each Rollout to report Healthy.
# The scheduler is still a Deployment and is covered by helm --wait above.
	@echo "==> waiting for rollouts"
	@set -e; for r in $$(kubectl get rollouts.argoproj.io -n $(NAMESPACE) -o name); do \
	  kubectl wait $$r -n $(NAMESPACE) --for=jsonpath='{.status.phase}'=Healthy --timeout=300s; \
	done

kind-status: _kind-guard ## Show nodes, releases and pods
	kubectl get nodes
	helm list -n $(NAMESPACE)
	kubectl get pods -n $(NAMESPACE) -o wide
	kubectl get rollouts.argoproj.io -n $(NAMESPACE)

kind-all: kind-up kind-platform kind-build kind-load kind-deploy ## Run the full local workflow

endif
