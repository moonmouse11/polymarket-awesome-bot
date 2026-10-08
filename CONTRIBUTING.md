# Contributing

Thanks for your interest in polymarket-awesome-bot! This guide covers the development setup and the checks a pull request has to pass.

## Development setup

Requirements: Go 1.27+, Docker with Docker Compose, `make`.

```bash
cp .env.example .env            # set MongoDB passwords: openssl rand -hex 24
docker compose up -d mongodb    # the database is enough for development
set -a; . ./.env; set +a        # Go does not read .env — export it into the shell
go run ./cmd/bot
```

`make help` lists all commands.

## Checks

Run before every commit:

```bash
make check   # lint + test
```

Or separately:

```bash
make lint    # golangci-lint: linters + formatting check (does not change files)
make fmt     # fix formatting (gofmt + goimports) in place
make test    # go test -race ./... with MONGO_TEST_URI from .env
```

### Tests

Tests in `internal/logger`, `internal/analyzer` and `internal/polymarket` need nothing external. Tests in `internal/db` run against a real MongoDB as the `polymarket_test` user in the separate `polymarket_test` database, which is dropped before and after the tests. The test user has no access to the bot's database.

If `MONGO_TEST_URI` is not set or MongoDB is unreachable, these tests are **skipped** locally. In CI (the `CI` variable is set) they **fail** instead, so a broken database cannot turn into a green run without tests.

Go caches test results; to force a rerun against the database use `go test -count=1 ./...`.

### Linter

[golangci-lint](https://golangci-lint.run) runs in a Docker image, nothing to install locally. Configuration: `.golangci.yml` — the standard set plus formatters:

| Linter | Finds |
|---|---|
| `govet` | Suspicious code from `go vet`: wrong `Printf` formats, copied mutexes, broken struct tags |
| `staticcheck` | Bugs, deprecated APIs, simplifications, style (e.g. capitalized error strings, `ST1005`) |
| `errcheck` | Unchecked errors. Ignore an error on purpose explicitly: `_ = f()` |
| `ineffassign` | Assignments whose result is never read |
| `unused` | Unused unexported functions, types, variables |
| `gofmt`, `goimports` | Formatting and import order |

The linter version lives in one place, `.golangci-lint-version`. Both the Makefile and CI read it, so local and CI results match.

## Pull requests

- Keep a pull request focused on one change.
- `make check` must pass. CI runs the same checks on every pull request: lint, tests with MongoDB, and a Docker image build.
- Update the README (both `README.md` and `README.ru.md`) when behavior, commands or configuration change.
- Never commit secrets: `.env` is ignored by git, use `.env.example` for new variables.

## Dependencies and versions

All versions are pinned exactly — Docker images, GitHub Actions (by commit SHA) and the linter. Dependabot opens weekly pull requests with updates; CI checks each one.

- **MongoDB:** Dependabot proposes only patch releases. Minor and major upgrades change the data file format (`featureCompatibilityVersion`) and are done by hand, step by step — CI tests run on an empty database and will not catch a failure to open existing data.
- **golangci-lint:** Dependabot does not update `.golangci-lint-version`; bump it by hand.

## Docker image

`.dockerignore` is an allowlist: only `go.mod`, `go.sum`, `cmd/` and `internal/` (without `*_test.go`) get into the build. **A new source directory has to be added to `.dockerignore`**, otherwise `docker build` fails with a missing package error.

The bot runs in the container as an unprivileged user (UID 10001), not root.
