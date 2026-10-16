.PHONY: help up down logs build clean markets-load markets-count markets-sample markets-find markets-show markets-awesome awesome-count awesome-sample sync-state rules rules-history rule-add rule-remove lint fmt test check notify-queue notify-backfill notify-reformat notify-edits-stop notify-deletes-stop notify-restore

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

# Go commands: `go run` where Go is installed (development), otherwise in the
# bot image (a server needs no Go toolchain). Force one with GO_CMD=docker|go.
GO_CMD ?= $(if $(shell command -v go),go,docker)
ifeq ($(GO_CMD),docker)
RUN_CMD = docker compose run --rm -T bot ./$(1)
else
RUN_CMD = go run ./cmd/$(1)
endif

markets-load: ## Full load of all markets (open, then closed) into Mongo
	@lvl="$$LOG_LEVEL"; fmt="$$LOG_FORMAT"; $(ENV); \
		LOG_LEVEL="$${lvl:-$$LOG_LEVEL}" LOG_FORMAT="$${fmt:-$$LOG_FORMAT}" $(call RUN_CMD,load-markets)

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

markets-awesome: ## Recompute awesome for all markets from filter_rules; queues deletion of posted markets no longer awesome
	@lvl="$$LOG_LEVEL"; fmt="$$LOG_FORMAT"; $(ENV); \
		LOG_LEVEL="$${lvl:-$$LOG_LEVEL}" LOG_FORMAT="$${fmt:-$$LOG_FORMAT}" $(call RUN_CMD,mark-awesome)

awesome-count: ## Awesome counts by reason, and which rules decided
	@$(ENV); $(MONGOSH) --eval 'const by = (reason, field) => db.markets.aggregate([{$$match: {awesome_reason: reason}}, {$$unwind: "$$awesome_matches." + field}, {$$group: {_id: "$$awesome_matches." + field, n: {$$sum: 1}}}, {$$sort: {n: -1}}]).toArray(); printjson({awesome: db.markets.countDocuments({is_awesome: true}), awesome_open: db.markets.countDocuments({is_awesome: true, closed: false}), not_checked: db.markets.countDocuments({is_awesome: {$$exists: false}}), by_reason: db.markets.aggregate([{$$group: {_id: "$$awesome_reason", n: {$$sum: 1}}}, {$$sort: {n: -1}}]).toArray(), allowed_by_tag: by("allow", "allow_tags"), allowed_by_word: by("allow", "allow_words"), denied_by_tag: by("deny", "deny_tags"), denied_by_word: by("deny", "deny_words")})'

awesome-sample: ## Show 10 random open awesome markets
	@$(ENV); $(MONGOSH) --eval 'db.markets.aggregate([{$$match: {is_awesome: true, closed: false}}, {$$sample: {size: 10}}, {$$project: {question: 1, tags: 1}}]).forEach(m => print(m._id + "  " + m.question + "  [" + (m.tags || []).join(", ") + "]"))'

notify-queue: ## Markets waiting for a Telegram notification, and the last sent
	@$(ENV); $(MONGOSH) --eval 'print("deletes pending:", db.markets.countDocuments({delete_pending: true}), "pending:", db.markets.countDocuments({notify_pending: true}), "edits pending:", db.markets.countDocuments({edit_pending: true}), "delete refused:", db.markets.countDocuments({telegram_delete_error: {$$exists: true}})); db.markets.find({notified_at: {$$exists: true}}, {question: 1, notified_at: 1}).sort({notified_at: -1}).limit(5).forEach(m => print(m.notified_at.toISOString() + "  " + m.question))'

notify-backfill: ## Queue all open awesome markets that were never announced (~20 messages/min)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({is_awesome: true, closed: false, notified_at: {$$exists: false}}, {$$set: {notify_pending: true}}); print("queued:", r.modifiedCount)'

notify-reformat: ## Re-render and edit all posted channel messages (after a format change; slow)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({telegram_message_id: {$$exists: true}}, {$$set: {edit_pending: true}}); print("queued edits:", r.modifiedCount)'

notify-edits-stop: ## Cancel all queued message edits (already edited messages stay as they are)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({edit_pending: true}, {$$unset: {edit_pending: ""}}); print("cancelled edits:", r.modifiedCount)'

notify-deletes-stop: ## Cancel all queued message deletions (messages stay in the channel)
	@$(ENV); $(MONGOSH) --eval 'const r = db.markets.updateMany({delete_pending: true}, {$$unset: {delete_pending: ""}}); print("cancelled deletions:", r.modifiedCount)'

notify-restore: ## After a lost DB: mark markets posted per a channel export: make notify-restore file=result.json
	@test -n "$(file)" || (echo "usage: make notify-restore file=result.json" && exit 1)
	@test -f "$(file)" || (echo "no such file: $(file)" && exit 1)
ifeq ($(GO_CMD),docker)
	@$(ENV); docker compose run --rm -T -v "$(abspath $(file)):/tmp/export.json:ro" bot ./restore-posts /tmp/export.json
else
	@$(ENV); go run ./cmd/restore-posts "$(file)"
endif

sync-state: ## Show polling progress (watermark, last successful cycle)
	@$(ENV); $(MONGOSH) --eval 'printjson(db.sync_state.find().toArray())'

# Filter rules live only in MongoDB (filter_rules, history in
# filter_rules_log). The bot reads them every cycle, so a change applies to
# markets updated from then on; `make markets-awesome` recomputes all.
# Values go through the environment, so spaces and quotes need no escaping.
rule-add rule-remove: export RULE_KIND = $(kind)
rule-add: export RULE_ACTION = $(action)
rule-add rule-remove: export RULE_VALUE = $(value)
rules-history: export RULE_N = $(n)

rule-add: ## Add a filter rule: make rule-add kind=tag|word action=allow|deny value="Soccer"
	@$(ENV); $(call RUN_CMD,rules) add "$$RULE_KIND" "$$RULE_ACTION" "$$RULE_VALUE"

rule-remove: ## Remove a filter rule: make rule-remove kind=tag|word value="Games"
	@$(ENV); $(call RUN_CMD,rules) remove "$$RULE_KIND" "$$RULE_VALUE"

rules: ## Show the filter rules (allow/deny tags and words)
	@$(ENV); $(call RUN_CMD,rules) list

rules-history: ## Last filter rule edits: make rules-history [n=50]
	@$(ENV); $(call RUN_CMD,rules) history $${RULE_N:-20}

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
