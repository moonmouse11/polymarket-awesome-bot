# polymarket-awesome-bot

[![CI](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/moonmouse11/polymarket-awesome-bot)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A bot that researches [Polymarket](https://polymarket.com) prediction markets: it finds *awesome* markets — unusual, strange, worth a closer look — and tracks how they change.

> [!NOTE]
> The project is in early development. Right now it loads and stores markets; detection and notifications are planned. Notification channels are a separate future part, and Telegram will be only one of them.

## Table of contents

- [Status](#status)
- [How it works](#how-it-works)
- [Getting started](#getting-started)
- [Configuration](#configuration)
- [Usage](#usage)
- [Contributing](#contributing)
- [Security](#security)
- [License](#license)

## Status

| Feature | State |
|---|---|
| Full load of all markets (open and closed) into MongoDB | ✅ done, manual command `make markets-load` |
| Awesome marking by tags (`make markets-awesome`) | ✅ draft rule: everything except excluded tags |
| `keywords` collection with initial seed | ✅ done |
| Health check (`/health`), graceful shutdown | ✅ done |
| Keyword analyzer (`KeywordAnalyzer`) | 🚧 code and tests exist, not wired into the bot yet |
| AI analyzer Jev | 🚧 stub, waiting for API early access |
| Polling for updates every minute | ✅ done, runs inside the bot |
| Change detectors for awesome markets | 📋 planned |
| Notifications (Telegram and other channels) | 📋 planned |
| Market translations (English / Russian) | 📋 planned |

## How it works

1. **Initial load.** All markets — open first, then closed — are fetched page by page from the Gamma API and stored in MongoDB. Runs manually: `make markets-load`.
2. **Updates.** Every `POLL_INTERVAL` (5 minutes by default) the bot fetches markets updated since the last run (newest `updatedAt` first, open and closed), writes them and recomputes their awesome flag. Progress is kept in the `sync_state` collection, so after downtime the bot catches up from where it stopped.
3. **Awesome markets.** A market is awesome unless it has one of the tags in the `excluded_tags` collection (sports, auto-generated series, elections, macro…). The rule is deliberately broad for now; `make markets-awesome` recomputes the flag for all markets.
4. **Detectors** *(planned).* Only for awesome markets that are still open: sharp price jumps, edits of the question or description, closing and resolution, volume spikes.

## Getting started

### Requirements

- Docker with Docker Compose
- Go 1.27+ — only for `make markets-load` and running the bot outside Docker
- `make`

### Run

```bash
git clone https://github.com/moonmouse11/polymarket-awesome-bot.git
cd polymarket-awesome-bot
cp .env.example .env   # set MongoDB passwords, see below
make up                # build and start the bot + MongoDB
make logs              # follow bot logs
make down              # stop
```

Check that the bot is alive:

```bash
curl http://127.0.0.1:8080/health   # OK
```

`make help` lists all commands.

> [!WARNING]
> `make clean` deletes the MongoDB data volume: all loaded markets and keyword edits are lost. MongoDB users are recreated from the passwords currently in `.env`.

### MongoDB users

MongoDB runs with authentication. Users are created **once**, on the first start with an empty volume (`docker/mongo-init/01-users.js`):

| User | Permissions | Used by |
|---|---|---|
| `root` (`MONGO_ROOT_*`) | everything | administration only |
| `polymarket_app` (`MONGO_APP_*`) | `readWrite` on `polymarket` only | the bot |
| `polymarket_test` (`MONGO_TEST_*`) | owner of `polymarket_test` only | tests |

- Docker Compose refuses to start without the passwords in `.env`.
- Use letters and digits only — passwords go into connection strings as is: `openssl rand -hex 24`.
- Changing a password in `.env` later does not change it in the database: change it in MongoDB by hand or recreate the volume.

Ports of the bot (`8080`) and MongoDB (`27017`) are published on `127.0.0.1` only and are not reachable from the network.

## Configuration

The bot is configured with environment variables. Docker Compose takes them from `.env`; Go itself does not read `.env`.

| Variable | Default | Description |
|---|---|---|
| `MONGO_URI` | `mongodb://localhost:27017` | Bot connection to MongoDB as `polymarket_app`. Docker Compose builds it from `MONGO_APP_*`; needed in `.env` for `go run` |
| `MONGO_ROOT_USER` / `MONGO_ROOT_PASSWORD` | `root` / — | MongoDB administrator (password required) |
| `MONGO_APP_USER` / `MONGO_APP_PASSWORD` | `polymarket_app` / — | Bot user (password required) |
| `MONGO_TEST_USER` / `MONGO_TEST_PASSWORD` | `polymarket_test` / — | Test user (password required) |
| `MONGO_TEST_URI` | — | Connection for `internal/db` tests |
| `PORT` | `8080` | Health check server port |
| `POLL_INTERVAL` | `5m` | How often the bot fetches updated markets (`90s`, `10m`, …) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` for log collection, `console` for readable local output |
| `JEV_API_KEY` | — | Reserved for the Jev analyzer, not used yet |
| `TELEGRAM_BOT_TOKEN` | — | Reserved for notifications, not used yet |

An invalid `LOG_LEVEL` or `LOG_FORMAT` stops the bot at startup.

## Usage

### Loading and exploring markets

```bash
make markets-load               # full load: open, then closed markets (takes a while)
make markets-count              # total / closed / active, time of the last write
make markets-sample             # 3 most recently updated markets
make markets-find Q=alien       # search question text (case-insensitive), up to 20
make markets-show ID=559651     # full market document
```

The load runs on the host, not in Docker, and needs Go. It is safe to rerun: markets are keyed by their Polymarket id, so a reload overwrites documents instead of duplicating them. If a load is interrupted, the pages already written stay — just run it again. A full load takes about 1.8 GB of MongoDB disk space.

### Awesome markets

```bash
make markets-awesome    # recompute is_awesome for all markets (a few minutes)
make awesome-count      # awesome / open awesome, and which tags excluded the rest
make awesome-sample     # 10 random open awesome markets
make sync-state         # polling progress: watermark and last successful cycle
make excluded-tags      # current excluded tags
```

Excluded tags live in the `excluded_tags` collection and are matched exactly as Polymarket spells them. Edit the list in the database, then run `make markets-awesome` again:

```bash
set -a; . ./.env; set +a
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.excluded_tags.insertOne({tag: "Mentions", created_at: new Date()})'
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.excluded_tags.deleteOne({tag: "Economy"})'
```

These commands read `MONGO_URI` from `.env` and run `mongosh` inside the `mongodb` container. Any query:

```bash
set -a; . ./.env; set +a
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.markets.find({tags: "Politics", closed: false}).limit(5)'
```

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, tests and checks. Run `make check` before opening a pull request.

## Security

Please do not report vulnerabilities in public issues. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © moonmouse11
