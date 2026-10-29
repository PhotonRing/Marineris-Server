GO       ?= go
VERSION  ?= dev
BIN      := bin/marineris

.PHONY: all ui build test lint check fuzz e2e integration images dev-server clean

all: ui build

ui:
	cd web && npm ci --no-audit --no-fund && npm run build

build:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) ./cmd/marineris

test:
	$(GO) test -race -count=1 ./...
	$(GO) test -count=1 -cover ./internal/... | scripts/coverage-gate.sh

lint:
	golangci-lint run --build-tags integration ./...
	scripts/check-forbidden-files.sh

# Every pre-push check in one command (runs Go in Docker when there is no Go).
check:
	scripts/check.sh

fuzz:
	@for t in $$(grep -rho 'func Fuzz[A-Za-z]*' --include=*_test.go internal | sed 's/func //' | sort -u); do \
	  pkg=$$(grep -rl "func $$t(" --include=*_test.go internal | xargs dirname | head -1); \
	  echo "== $$t ($$pkg)"; $(GO) test ./$$pkg -run '^$$' -fuzz "^$$t\$$" -fuzztime 30s || exit 1; \
	done

e2e: ui build
	cd test/e2e && npm ci --no-audit --no-fund && npx playwright test

integration:
	test/integration/run.sh

images:
	docker build --build-arg VERSION=$(VERSION) -t marineris:$(VERSION) .
	docker build -t marineris-caddy:$(VERSION) deploy/caddy

dev-server: build
	mkdir -p .dev-data
	MARINERIS_DATA_DIR=$(CURDIR)/.dev-data MARINERIS_LOCAL_ADDR=127.0.0.1:8081 MARINERIS_EDGE_ADDR=127.0.0.1:8080 \
	MARINERIS_DOCKER_HOST=$${DOCKER_HOST:-unix:///var/run/docker.sock} \
	$(BIN) serve

clean:
	rm -rf bin .dev-data web/node_modules test/e2e/node_modules test/e2e/.data
