.DEFAULT_GOAL := help

GO          ?= go
GOLANGCI    ?= golangci-lint
PKG         := ./...
COVER_FILE  := coverage.txt
COVER_HTML  := coverage.html

# -------------------------------------------------
# Macros
# -------------------------------------------------

# parse_version: sets MAJOR, MINOR, PATCH from the latest git tag.
# Falls back to 0.0.0 if no tags exist.
define parse_version
	LATEST=$$(git describe --tags --abbrev=0 2>/dev/null || echo ""); \
	V=$$(echo "$${LATEST:-v0.0.0}" | sed 's/^v//'); \
	IFS='.' read -r MAJOR MINOR PATCH <<< "$$V"
endef

# new_version: tags and pushes $$NEW, rolls back the local tag on push failure.
define new_version
	echo "🚀 New version: $$NEW"; \
	git tag "$$NEW" && git push origin "$$NEW" || (git tag -d "$$NEW"; exit 1); \
	echo "✅ Tag $$NEW created and pushed"
endef


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

# -------------------------------------------------
# Automatic version bumps
# -------------------------------------------------
.PHONY: tag-patch
tag-patch:
	@$(parse_version); \
	NEW="v$$MAJOR.$$MINOR.$$((PATCH + 1))"; \
	$(new_version)

.PHONY: tag-minor
tag-minor:
	@$(parse_version); \
	NEW="v$$MAJOR.$$((MINOR + 1)).0"; \
	$(new_version)

.PHONY: tag-major
tag-major:
	@$(parse_version); \
	NEW="v$$((MAJOR + 1)).0.0"; \
	$(new_version)

.PHONY: install-tools
install-tools: ## Install development tools (golangci-lint)
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest