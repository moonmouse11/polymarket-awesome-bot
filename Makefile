.PHONY: help up down logs build clean markets-load markets-count markets-sample markets-find markets-show markets-awesome awesome-count awesome-sample sync-state excluded-tags lint fmt test check notify-queue notify-backfill notify-reformat notify-edits-stop

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

markets-awesome: ## Recompute is_awesome for all markets from excluded_tags
	@lvl="$$LOG_LEVEL"; fmt="$$LOG_FORMAT"; $(ENV); \
		LOG_LEVEL="$${lvl:-$$LOG_LEVEL}" LOG_FORMAT="$${fmt:-$$LOG_FORMAT}" go run ./cmd/mark-awesome

awesome-count: ## Awesome counts by reason, overriding words and excluded tags
	@$(ENV); $(MONGOSH) --eval 'printjson({awesome: db.markets.countDocuments({is_awesome: true}), awesome_open: db.markets.countDocuments({is_awesome: true, closed: false}), not_checked: db.markets.countDocuments({is_awesome: {$$exists: false}}), by_reason: db.markets.aggregate([{$$group: {_id: "$$awesome_reason", n: {$$sum: 1}}}, {$$sort: {n: -1}}]).toArray(), override_words: db.markets.aggregate([{$$match: {awesome_reason: "words"}}, {$$unwind: "$$awesome_words"}, {$$group: {_id: "$$awesome_words", n: {$$sum: 1}}}, {$$sort: {n: -1}}]).toArray(), excluded_by: db.markets.aggregate([{$$match: {is_awesome: false}}, {$$group: {_id: "$$awesome_excluded_tag", n: {$$sum: 1}}}, {$$sort: {n: -1}}]).toArray()})'

awesome-sample: ## Show 10 random open awesome markets
	@$(ENV); $(MONGOSH) --eval 'db.markets.aggregate([{$$match: {is_awesome: true, closed: false}}, {$$sample: {size: 10}}, {$$project: {question: 1, tags: 1}}]).forEach(m => print(m._id + "  " + m.question + "  [" + (m.tags || []).join(", ") + "]"))'

notify-queue: ## Markets waiting for a Telegram notification, and the last sent
	@$(ENV); $(MONGOSH) --eval 'print("pending:", db.markets.countDocuments({notify_pending: true}), "edits pending:", db.markets.countDocuments({edit_pending: true})); db.markets.find({notified_at: {$$exists: true}}, {question: 1, notified_at: 1}).sort({notified_at: -1}).limit(5).forEach(m => print(m.notified_at.toISOString() + "  " + m.question))'

notify-backfill: ## Queue all open awesome markets that were never announced (~20 messages/min)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({is_awesome: true, closed: false, notified_at: {$$exists: false}}, {$$set: {notify_pending: true}}); print("queued:", r.modifiedCount)'

notify-reformat: ## Re-render and edit all posted channel messages (after a format change; slow)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({telegram_message_id: {$$exists: true}}, {$$set: {edit_pending: true}}); print("queued edits:", r.modifiedCount)'

notify-edits-stop: ## Cancel all queued message edits (already edited messages stay as they are)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({edit_pending: true}, {$$unset: {edit_pending: ""}}); print("cancelled edits:", r.modifiedCount)'

sync-state: ## Show polling progress (watermark, last successful cycle)
	@$(ENV); $(MONGOSH) --eval 'printjson(db.sync_state.find().toArray())'

excluded-tags: ## List tags that make a market not awesome
	@$(ENV); $(MONGOSH) --eval 'db.excluded_tags.find({}, {_id: 0, tag: 1}).sort({tag: 1}).forEach(t => print(t.tag))'

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
