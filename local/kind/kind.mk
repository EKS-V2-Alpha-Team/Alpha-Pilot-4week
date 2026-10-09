# Include guard: safe if this file is included more than once.
ifndef KIND_MK_INCLUDED
KIND_MK_INCLUDED := 1

# kind targets for local Kubernetes testing. Run from the repo root, either:
#   make kind-help                        (via the root Makefile's include)
#   make -f local/kind/kind.mk            (standalone; shows kind-help by default,
#                                          because kind-help is the first target)
# Keep kind-help as the first target in this file.
#
# The Helm install below MIRRORS the Argo CD ApplicationSet
# (kubernetes/platform/apps/ -> order-platform-<env>):
#   release name = service folder name
#   namespace    = order-platform
#   valueFiles   = services/<svc>/values.yaml
#                  envs/<env>/common.yaml
#                  envs/<env>/<svc>.yaml
# If the ApplicationSet's valueFiles change, change _helm-values here too,
# otherwise kind tests a config Argo CD never deploys.

KIND_DIR          := local/kind
KIND_CLUSTER      ?= eks-v2-local
KIND_CONTEXT      := kind-$(KIND_CLUSTER)

# Matches EKS: infra/terraform/modules/eks/main.tf (version = "1.35").
# If EKS is upgraded, update this to the same minor version, pinned by digest.
# Images per kind release: https://github.com/kubernetes-sigs/kind/releases
KIND_NODE_IMAGE   ?= kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0

# Repo layout (paths relative to the repo root).
K8S_DIR           := kubernetes
CHART             := $(K8S_DIR)/charts/microservice
SERVICES_DIR      := $(K8S_DIR)/deploy/services
ENVS_DIR          := $(K8S_DIR)/deploy/envs
PLATFORM_DIR      := $(K8S_DIR)/platform

# Platform pieces the services need, applied from the SAME folders the
# Argo CD Applications in platform/apps/ use (plain manifests, namespace
# order-platform). AWS-only apps (Karpenter, LB controller, external-dns,
# ESO's AWS store) are deliberately not installed in kind.
KIND_PLATFORM     ?= postgres redis

# The Postgres StatefulSet reads POSTGRES_USER / POSTGRES_PASSWORD from this
# Secret. On EKS it comes from elsewhere; in kind we create a local one with
# throwaway dev credentials. Key names must match the secretKeyRef keys in
# kubernetes/platform/postgres/statefulset.yaml.
PG_SECRET         ?= postgres-credentials
PG_USER_KEY       ?= username
PG_PASS_KEY       ?= password
# Same user/password as docker-compose.yml and DATABASE_URL in values-kind.yaml.
PG_LOCAL_USER     ?= app
PG_LOCAL_PASS     ?= localdev

# metrics-server via its official Helm chart (pinned), for `kubectl top` and HPA.
# Releases: https://github.com/kubernetes-sigs/metrics-server/releases
METRICS_SERVER_REPO    := https://kubernetes-sigs.github.io/metrics-server/
METRICS_SERVER_VERSION ?= 3.14.0

# Traefik: SAME chart version and values as Argo CD. The version is read from
# the Argo CD Application so kind can't drift from EKS.
# (Reloader is not installed: on EKS it only restarts pods when External
# Secrets updates a Secret, and kind has no External Secrets.)
APPS_DIR          := $(PLATFORM_DIR)/apps
HELM_VALUES_DIR   := $(K8S_DIR)/helm-values
argo_version       = $(shell awk '/targetRevision:/{print $$2; exit}' $(APPS_DIR)/$(1).yaml)
TRAEFIK_REPO      := https://traefik.github.io/charts
TRAEFIK_VERSION   ?= $(call argo_version,traefik)

# floci (local SQS) init script, shared with docker-compose.yml.
FLOCI_INIT        ?= scripts/floci-init.sh
VALUES_KIND       := $(KIND_DIR)/values-kind.yaml

# Which environment's values to test. Only envs that exist under
# kubernetes/deploy/envs/ are valid (currently: dev).
ENV               ?= dev

# Same namespace as the ApplicationSet. One environment at a time:
# switching ENV re-deploys the same releases with that env's values.
NAMESPACE         ?= order-platform

# Services are discovered from the folders, exactly like the Argo CD git
# generator. Override to deploy a subset: SERVICES="order-service worker"
SERVICES          ?= $(notdir $(patsubst %/,%,$(wildcard $(SERVICES_DIR)/*/)))

# Local images. Source folders are the same ones docker-compose.yml builds.
# Each service becomes order-platform-local:<svc>-local. (The chart schema
# rejects the tag "latest", so a fixed "-local" tag is used instead.)
APP_SRC_DIR       ?= services
LOCAL_REPO        := order-platform-local
local_image        = $(LOCAL_REPO):$(1)-local

# Value files for one service ($(1)) in one env ($(2)), in ApplicationSet order,
# plus the kind overlay last.
define _helm-values
-f $(SERVICES_DIR)/$(1)/values.yaml \
-f $(ENVS_DIR)/$(2)/common.yaml \
-f $(ENVS_DIR)/$(2)/$(1).yaml \
-f $(VALUES_KIND) \
--set image.tag=$(1)-local
endef

# Resource kinds that need CRDs or AWS that kind doesn't have. If any of these
# appear in rendered output, values-kind.yaml has stopped switching them off.
KIND_FORBIDDEN    := ExternalSecret|Certificate|ServiceMonitor

.PHONY: kind-help kind-check kind-up kind-down kind-clean kind-load kind-platform \
        kind-render kind-build kind-deploy kind-test kind-status kind-all _kind-guard _kind-env

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

kind-up: kind-check ## Create the local cluster (no-op if it exists)
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

kind-clean: kind-down ## Delete the cluster AND the locally built service images (frees disk)
	@imgs=$$(docker images $(LOCAL_REPO) -q | sort -u); \
	if [ -n "$$imgs" ]; then docker rmi -f $$imgs; else echo "No $(LOCAL_REPO) images to remove"; fi

# Safety: refuse to touch any cluster that isn't the kind one.
_kind-guard:
	@ctx=$$(kubectl config current-context); \
	if [ "$$ctx" != "$(KIND_CONTEXT)" ]; then \
	  echo "Refusing: current context is '$$ctx', expected '$(KIND_CONTEXT)'"; exit 1; \
	fi

# Fail early if the chosen environment has no values folder yet.
_kind-env:
	@test -d $(ENVS_DIR)/$(ENV) || { \
	  echo "No $(ENVS_DIR)/$(ENV)/ - available: $$(ls $(ENVS_DIR))"; exit 1; }

kind-build: ## Build images for SERVICES from services/<svc> (same as docker compose)
	@set -e; $(foreach svc,$(SERVICES), \
	  echo "==> building $(svc)"; \
	  docker build -t $(call local_image,$(svc)) $(APP_SRC_DIR)/$(svc);)

kind-load: _kind-guard ## Load the built images into the cluster
	kind load docker-image --name $(KIND_CLUSTER) \
	  $(foreach svc,$(SERVICES),$(call local_image,$(svc)))

kind-platform: _kind-guard ## Postgres, Redis, Traefik (same as Argo CD), floci, metrics-server
	kubectl create namespace $(NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -
	@# kind-only stand-ins for things EKS provides (see the files for why)
	kubectl apply -f $(KIND_DIR)/storageclass-gp3.yaml
	kubectl create secret generic $(PG_SECRET) -n $(NAMESPACE) \
	  --from-literal=$(PG_USER_KEY)=$(PG_LOCAL_USER) \
	  --from-literal=$(PG_PASS_KEY)=$(PG_LOCAL_PASS) \
	  --dry-run=client -o yaml | kubectl apply -f -
	@test -f $(FLOCI_INIT) || { echo "missing $(FLOCI_INIT) (floci init script)"; exit 1; }
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
	@set -e; for app in $(KIND_PLATFORM); do \
	  dir=$(PLATFORM_DIR)/$$app; \
	  echo "==> $$app"; \
	  kubectl apply -n $(NAMESPACE) -R -f $$dir; \
	done
	@echo "==> waiting for platform pods"
	kubectl rollout status deployment/metrics-server -n kube-system --timeout=180s
	@for ns in traefik; do \
	  for r in $$(kubectl get deployment -n $$ns -o name); do \
	    kubectl rollout status $$r -n $$ns --timeout=180s; \
	  done; \
	done
	@for kind in deployment statefulset; do \
	  for r in $$(kubectl get $$kind -n $(NAMESPACE) -o name); do \
	    kubectl rollout status $$r -n $(NAMESPACE) --timeout=180s; \
	  done; \
	done

kind-render: ## Render every service for every env, and check nothing AWS-only leaks in
	@set -e; for env in $$(ls $(ENVS_DIR)); do \
	  for svc in $(SERVICES); do \
	    printf '  %-8s %-22s ' $$env $$svc; \
	    out=$$(helm template $$svc $(CHART) \
	      -f $(SERVICES_DIR)/$$svc/values.yaml \
	      -f $(ENVS_DIR)/$$env/common.yaml \
	      -f $(ENVS_DIR)/$$env/$$svc.yaml \
	      -f $(VALUES_KIND) \
	      --set image.tag=$$svc-local); \
	    found=$$(echo "$$out" | grep -oE '^kind: ($(KIND_FORBIDDEN))$$' | sort -u | tr '\n' ' '); \
	    if [ -n "$$found" ]; then echo "FAIL: renders $$found"; exit 1; fi; \
	    if echo "$$out" | grep -q 'dkr.ecr'; then echo "FAIL: still points at ECR"; exit 1; fi; \
	    echo ok; \
	  done; \
	done
	@echo "All services render for kind"

kind-deploy: _kind-guard _kind-env ## Deploy all SERVICES with ENV values into NAMESPACE
	@set -e; $(foreach svc,$(SERVICES), \
	  echo "==> $(svc) ($(ENV))"; \
	  helm upgrade --install $(svc) $(CHART) \
	    --namespace $(NAMESPACE) --create-namespace \
	    $(call _helm-values,$(svc),$(ENV)) \
	    --wait --timeout 5m;)

kind-test: _kind-guard ## Smoke test every Deployment in NAMESPACE
	bash $(KIND_DIR)/scripts/smoke-test.sh $(NAMESPACE)

kind-status: _kind-guard ## Show nodes, releases and pods
	kubectl get nodes
	helm list -n $(NAMESPACE)
	kubectl get pods -n $(NAMESPACE) -o wide

kind-all: kind-up kind-platform kind-build kind-load kind-deploy kind-test ## Full run: up -> platform -> build -> load -> deploy -> test

endif
