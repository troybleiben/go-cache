.DEFAULT_GOAL := help

GO          ?= go
GOLANGCI    ?= golangci-lint
PKG         := ./...
COVER_FILE  := coverage.txt
COVER_HTML  := coverage.html

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n\nTargets:\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: fmt
fmt: ## Format code with gofmt
	$(GO) fmt $(PKG)

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: lint
lint: ## Run golangci-lint
	$(GOLANGCI) run

.PHONY: test
test: ## Run tests with race detector
	$(GO) test -race -count=1 $(PKG)

.PHONY: test-short
test-short: ## Run tests without race detector (fast)
	$(GO) test -count=1 $(PKG)

.PHONY: cover
cover: ## Run tests with coverage report
	$(GO) test -race -count=1 -coverprofile=$(COVER_FILE) -covermode=atomic $(PKG)
	@$(GO) tool cover -func=$(COVER_FILE) | tail -1

.PHONY: cover-html
cover-html: cover ## Generate HTML coverage report
	$(GO) tool cover -html=$(COVER_FILE) -o $(COVER_HTML)
	@echo "HTML report: $(COVER_HTML)"

.PHONY: bench
bench: ## Run benchmarks
	$(GO) test -bench=. -benchmem -run=^$$ $(PKG)

.PHONY: examples
examples: ## Build and run all examples
	$(GO) run ./examples/basic
	@echo "---"
	$(GO) run ./examples/multiindex

.PHONY: build-examples
build-examples: ## Verify examples compile
	$(GO) build ./examples/...

.PHONY: check
check: tidy fmt vet lint test ## Run all checks (tidy, fmt, vet, lint, test)

.PHONY: ci
ci: vet test cover ## CI pipeline: vet + race tests + coverage

.PHONY: clean
clean: ## Remove generated files
	rm -f $(COVER_FILE) $(COVER_HTML)
	$(GO) clean -testcache

# ---- Release tagging ----
# Computes the next semver tag from the latest one and pushes it.
# Working tree must be clean and on the default branch.
# Use DRY_RUN=1 to preview without tagging:  make tag-patch DRY_RUN=1

LATEST_TAG := $(shell git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null || echo v0.0.0)

.PHONY: current-version
current-version: ## Show the latest semver tag
	@echo $(LATEST_TAG)

.PHONY: tag-patch
tag-patch: ## Tag and push next PATCH release (e.g. v0.1.0 -> v0.1.1)
	@$(MAKE) --no-print-directory _tag BUMP=patch

.PHONY: tag-minor
tag-minor: ## Tag and push next MINOR release (e.g. v0.1.3 -> v0.2.0)
	@$(MAKE) --no-print-directory _tag BUMP=minor

.PHONY: tag-major
tag-major: ## Tag and push next MAJOR release (e.g. v0.4.2 -> v1.0.0)
	@$(MAKE) --no-print-directory _tag BUMP=major

.PHONY: tag
tag: ## Tag and push an explicit version (usage: make tag VERSION=v0.2.0)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make tag VERSION=v0.2.0"; exit 1; fi
	@$(MAKE) --no-print-directory _tag NEW_VERSION=$(VERSION)

.PHONY: _tag
_tag:
	@set -e; \
	current="$(LATEST_TAG)"; \
	if [ -n "$(NEW_VERSION)" ]; then \
		new="$(NEW_VERSION)"; \
	else \
		ver=$${currentv}; \
		major=$$(echo $$ver | cut -d. -f1); \
		minor=$$(echo $$ver | cut -d. -f2); \
		patch=$$(echo $$ver | cut -d. -f3 | cut -d- -f1); \
		case "$(BUMP)" in \
			patch) patch=$$((patch + 1));; \
			minor) minor=$$((minor + 1)); patch=0;; \
			major) major=$$((major + 1)); minor=0; patch=0;; \
			*) echo "Error: unknown BUMP=$(BUMP)"; exit 1;; \
		esac; \
		new="v$$major.$$minor.$$patch"; \
	fi; \
	if ! echo "$$new" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$$'; then \
		echo "Error: $$new is not a valid semver tag"; exit 1; \
	fi; \
	if git rev-parse "$$new" >/dev/null 2>&1; then \
		echo "Error: tag $$new already exists"; exit 1; \
	fi; \
	if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: working tree is dirty; commit or stash first"; exit 1; \
	fi; \
	branch=$$(git rev-parse --abbrev-ref HEAD); \
	if [ "$$branch" != "main" ] && [ "$$branch" != "master" ]; then \
		echo "Warning: tagging from branch '$$branch' (not main/master)"; \
	fi; \
	echo "Current: $$current"; \
	echo "New:     $$new"; \
	if [ "$(DRY_RUN)" = "1" ]; then \
		echo "(dry run — no tag created)"; exit 0; \
	fi; \
	git tag -a "$$new" -m "Release $$new"; \
	git push origin "$$new"; \
	echo "Pushed tag $$new. pkg.go.dev should pick it up shortly."

.PHONY: install-tools
install-tools: ## Install development tools (golangci-lint)
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest