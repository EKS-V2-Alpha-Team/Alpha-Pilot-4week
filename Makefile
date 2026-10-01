SHELL := /bin/bash

# Target one service:  make test SERVICE=order-service
SERVICE ?=
SVC_DIRS := $(if $(SERVICE),services/$(SERVICE),$(wildcard services/*))

# Run a command in every service that has a go.mod
define each_service
	@set -e; for d in $(SVC_DIRS); do \
	    if [ -f $$d/go.mod ]; then echo "==> $$d"; (cd $$d && $(1)); fi; \
	done
endef

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-22s\033[0m %s\n", $$1, $$2}'

.PHONY: install
install: ## Install app dependencies
	$(call each_service,go mod download)

.PHONY: lint
lint: ## Lint Go code (SERVICE=name for one)
	$(call each_service,golangci-lint run ./...)

.PHONY: test
test: ## Run Go tests (SERVICE=name for one)
	$(call each_service,go test ./...)

.PHONY: up
up: ## Start everything, or rebuild one (SERVICE=name)
	docker compose up -d --build $(SERVICE)

.PHONY: down
down: ## Stop everything, or one (SERVICE=name)
	$(if $(SERVICE),docker compose stop $(SERVICE),docker compose down -v)

.PHONY: logs
logs: ## Tail logs (SERVICE=name for one)
	docker compose logs -f $(SERVICE)