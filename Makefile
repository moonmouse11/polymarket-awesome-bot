.PHONY: help up down logs build clean markets-load markets-count markets-sample markets-find markets-show lint fmt test check

.DEFAULT_GOAL := help

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build docker images
	docker compose build

up: ## Start containers in background
	docker compose up --build -d

down: ## Stop containers
	docker compose down

logs: ## Tail container logs
	docker compose logs -f bot

clean: ## Stop containers and remove volumes
	docker compose down -v
	rm -rf bin/

# --- Markets (read .env for MONGO_URI; mongosh runs inside the mongodb container) ---
ENV := set -a; . ./.env; set +a
MONGOSH := docker compose exec -T mongodb mongosh --quiet "$$MONGO_URI"

markets-load: ## Full load of all markets (open, then closed) into Mongo
	@lvl="$$LOG_LEVEL"; fmt="$$LOG_FORMAT"; $(ENV); \
		LOG_LEVEL="$${lvl:-$$LOG_LEVEL}" LOG_FORMAT="$${fmt:-$$LOG_FORMAT}" go run ./cmd/load-markets

markets-count: ## Count markets: total / open / closed / active
	@$(ENV); $(MONGOSH) --eval 'printjson({total: db.markets.countDocuments(), closed: db.markets.countDocuments({closed: true}), active: db.markets.countDocuments({active: true}), last_sync: db.markets.find({}, {synced_at: 1}).sort({synced_at: -1}).limit(1).toArray()[0]?.synced_at})'

markets-sample: ## Show 3 most recently updated markets
	@$(ENV); $(MONGOSH) --eval 'printjson(db.markets.find({}, {question: 1, tags: 1, outcomes: 1, outcome_prices: 1, volume: 1, closed: 1, updated_at: 1}).sort({updated_at: -1}).limit(3).toArray())'

markets-find: ## Find markets by question text: make markets-find Q=alien
	@test -n "$(Q)" || (echo "usage: make markets-find Q=alien" && exit 1)
	@$(ENV); docker compose exec -T -e Q='$(Q)' mongodb mongosh --quiet "$$MONGO_URI" --eval 'printjson(db.markets.find({question: {$$regex: process.env.Q, $$options: "i"}}, {question: 1, outcome_prices: 1, volume: 1, closed: 1}).limit(20).toArray())'

markets-show: ## Show one full market document: make markets-show ID=559651
	@test -n "$(ID)" || (echo "usage: make markets-show ID=559651" && exit 1)
	@$(ENV); docker compose exec -T -e ID='$(ID)' mongodb mongosh --quiet "$$MONGO_URI" --eval 'printjson(db.markets.findOne({_id: process.env.ID}))'

# --- Code quality (golangci-lint runs in Docker, same image as CI) ---
# Pinned version: the same command must give the same result locally and in CI.
# Single source of the linter version, shared with golangci-lint-action in CI.
LINT_IMAGE := golangci/golangci-lint:$(shell cat .golangci-lint-version)
# Named volumes keep downloaded modules and lint cache between runs.
LINT_CACHE := -v polymarket-lint-gomod:/go/pkg/mod -v polymarket-lint-cache:/root/.cache

lint: ## Run linters (golangci-lint standard set + gofmt/goimports check)
	docker run --rm $(LINT_CACHE) -v "$(CURDIR)":/app:ro -w /app $(LINT_IMAGE) golangci-lint run ./...

fmt: ## Fix formatting in place (gofmt + goimports)
	docker run --rm $(LINT_CACHE) -v "$(CURDIR)":/app -w /app $(LINT_IMAGE) golangci-lint fmt ./...

test: ## Run all tests with race detector (DB tests use MONGO_TEST_URI from .env)
	@$(ENV); go test -race ./...

check: lint test ## Pre-commit check: lint + tests
