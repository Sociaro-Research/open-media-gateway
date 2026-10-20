.PHONY: help build build-ui test test-race test-isolated test-recovery performance benchmark vet mod-check vulncheck fmt fmt-check verify verify-ci release-check up down logs

help:
	@echo "Media Gateway development commands:"
	@echo "  make verify     Check modules, formatting, vet, vulnerabilities, tests, and builds"
	@echo "  make verify-ci  Run verify with required isolated PostgreSQL and Redis"
	@echo "  make release-check  Check the Git tree for public-release mistakes"
	@echo "  make test-isolated  Run verify-ci with disposable native PostgreSQL and Redis"
	@echo "  make test-recovery  Run dedicated native recovery and real worker lease tests"
	@echo "  make performance    Run opt-in load tests against isolated test service URLs"
	@echo "  make benchmark      Run CPU/allocation benchmarks without race instrumentation"
	@echo "  make test       Run the unit test suite"
	@echo "  make test-race  Run tests with the race detector"
	@echo "  make vulncheck  Check reachable known Go vulnerabilities"
	@echo "  make fmt        Format Go source files"
	@echo "  make up         Build and start the local stack"
	@echo "  make down       Stop the local stack"

build-ui:
	./scripts/build-ui.sh

build:
	go build ./cmd/gateway ./cmd/worker ./cmd/webhook-receiver

test:
	go test ./...

test-race:
	go test -race $(TEST_FLAGS) ./...

vet:
	go vet ./...

mod-check:
	go mod tidy -diff

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

fmt:
	gofmt -w cmd docs internal plugins

fmt-check:
	@test -z "$$(gofmt -l cmd docs internal plugins)" || \
		(echo "Go files need formatting. Run: make fmt"; gofmt -l cmd docs internal plugins; exit 1)

verify: mod-check fmt-check vet vulncheck test-race build

verify-ci:
	@test -n "$$GATEWAY_TEST_DATABASE_URL" -a -n "$$GATEWAY_TEST_REDIS_URL" || \
		(echo "Set GATEWAY_TEST_DATABASE_URL and GATEWAY_TEST_REDIS_URL to isolated test services; integration tests must not be skipped."; exit 1)
	$(MAKE) verify

test-isolated:
	python3 scripts/with-test-services.py -- $(MAKE) verify-ci TEST_FLAGS='-count=1'

test-recovery:
	python3 scripts/with-test-services.py -- env GATEWAY_NATIVE_RECOVERY=1 GATEWAY_REAL_LEASE_EXPIRY=1 go test -race -count=1 -timeout=10m -v -run '^TestReliability' ./internal/reliability

performance:
	@test -n "$$GATEWAY_TEST_DATABASE_URL" -a -n "$$GATEWAY_TEST_REDIS_URL" -a -n "$$GATEWAY_PERF_OUTPUT" || \
		(echo "Set isolated test service URLs and GATEWAY_PERF_OUTPUT; see docs/performance-testing.md"; exit 1)
	GATEWAY_PERF=1 go test -count=1 -timeout=20m -v -run TestPerformance ./internal/reliability

benchmark:
	go test -run '^$$' -bench 'Benchmark(MatchUsageBills(Shapes|BroadEnvelope)?|ReportSummary)$$' -benchmem -benchtime=1x -count=3 ./internal/core

release-check:
	./scripts/public-release-check.sh

up:
	docker compose --profile ui up --build

down:
	docker compose down

logs:
	docker compose logs -f gateway worker
