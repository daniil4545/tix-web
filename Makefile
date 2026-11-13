# Gate of ~/.claude/templates/Makefile.go without a database and paid runs. GitHub flow: the base is main.
# Needs go, golangci-lint, uv and node; the full tier also needs tix 0.9.1 in PATH.
.PHONY: build test test-fast vet fmt fmt-check golangci infra-check lint secrets security test-guard pr-size check push-check pr-check ci-check

GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@v1.9.0
GITLEAKS := go run github.com/zricethezav/gitleaks/v8@v8.30.1
BASE ?= origin/main
# Tests run without the machine git config, as on a CI runner.
GIT_BARE := GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1

build:
	go build -o bin/tix-web .

# Fast tier: fake tix only.
test-fast:
	go test -short ./... $(if $(RUN),-run $(RUN) -count=1,)

# Full tier: the e2e test on real tix 0.9.1 fails without it instead of skipping.
test:
	$(GIT_BARE) go test -count=1 ./... $(if $(RUN),-run $(RUN),)

vet:
	go vet ./...

fmt:
	golangci-lint fmt

fmt-check:
	golangci-lint fmt --diff

golangci:
	golangci-lint run --timeout 120s

# Shell, CI, JS and HTML (scripts/lint-files.sh).
infra-check:
	scripts/lint-files.sh

lint: fmt-check vet golangci infra-check

# gitleaks ignores .gitignore, so it scans a snapshot of what git sees instead of local files.
secrets:
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	root=$$(git rev-parse --show-toplevel); \
	cp "$$(git rev-parse --git-path index)" "$$tmp/index"; \
	GIT_INDEX_FILE="$$tmp/index" git -C "$$root" add -A; \
	tree=$$(GIT_INDEX_FILE="$$tmp/index" git -C "$$root" write-tree); \
	mkdir "$$tmp/tree"; git -C "$$root" archive "$$tree" | tar -x -C "$$tmp/tree"; \
	cd "$$tmp/tree" && $(GITLEAKS) dir --no-banner --redact .

security: secrets
	$(GOVULNCHECK) ./...

# A weakened or deleted assertion needs TEST_CHANGE='<reason>' and a `Test-change:` line.
test-guard:
	scripts/test-guard.py --base $(BASE)

# Lines of code a PR carries, limit 400; PR_SIZE_MAX raises it.
pr-size:
	scripts/pr-size.py --base $(BASE)

# Acceptance: static checks, both tiers, build.
check: lint test build

push-check: lint secrets test-fast test-guard

pr-check: lint secrets test-fast build pr-size test-guard

ci-check: lint security test build test-guard
