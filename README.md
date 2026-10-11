# polymarket-awesome-bot

[![CI](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/moonmouse11/polymarket-awesome-bot)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Telegram channel](https://img.shields.io/badge/Telegram-@polymarket__search__awesome-26A5E4?logo=telegram)](https://t.me/polymarket_search_awesome)

A bot that researches [Polymarket](https://polymarket.com) prediction markets: it finds *awesome* markets — unusual, strange, worth a closer look — and tracks how they change.

## Live service

New awesome markets are posted to the Telegram channel **[Polymarket Search](https://t.me/polymarket_search_awesome)** ([@polymarket_search_awesome](https://t.me/polymarket_search_awesome)) by the bot [@polymarket_search_awesome_bot](https://t.me/polymarket_search_awesome_bot). Subscribe to the channel to get them; the bot itself only posts and does not answer messages.

> [!NOTE]
> The project is in early development. Right now it loads and stores markets, marks awesome ones and announces new awesome markets in a Telegram channel; change detectors are planned.

## Table of contents

- [Live service](#live-service)
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
| Awesome marking by filter rules (`make markets-awesome`) | ✅ allow/deny tags and words; awesome unless denied |
| Filter rules: allow/deny lists of tags and words in MongoDB (`filter_rules`), edited with `make`, with history | ✅ done |
| Health check (`/health`), graceful shutdown | ✅ done |
| Keyword analyzer (`KeywordAnalyzer`) | 🚧 code and tests exist, not wired into the bot yet |
| AI analyzer Jev | 🚧 stub, waiting for API early access |
| Polling for updates every `POLL_INTERVAL` | ✅ done, runs inside the bot |
| Admin commands `/status`, `/health_check` and health alerts in Telegram | ✅ done |
| Change detectors for awesome markets | 📋 planned |
| Telegram channel: new awesome markets | ✅ done |
| Market translations (English / Russian) | 📋 planned |

## How it works

1. **Initial load.** All markets — open first, then closed — are fetched page by page from the Gamma API and stored in MongoDB. Runs manually: `make markets-load`.
2. **Updates.** Every `POLL_INTERVAL` (5 minutes by default) the bot fetches markets updated since the last run (newest `updatedAt` first, open and closed), writes them and recomputes their awesome flag. Progress is kept in the `sync_state` collection, so after downtime the bot catches up from where it stopped.
3. **Awesome markets.** Filter rules from the `filter_rules` collection decide: a market that matches an allow tag or word is awesome; otherwise one that matches a deny tag or word is not; a market that matches nothing is awesome, so nothing interesting is missed — noise is cut with deny rules. Every matched rule is stored in `awesome_matches`. The rule is deliberately broad for now; `make markets-awesome` recomputes the flag for all markets.
4. **Notifications.** When an open market becomes awesome (a new market, or an old one after a filter rule edit), it is queued in MongoDB (`notify_pending`) and posted to the Telegram channel, at most ~20 messages a minute. A message shows the odds, volume, end date, why the market passed (✅ not filtered — no deny rule matched, or 🔑 the allow tags/words it matched, with the deny rules they override), its tags as hashtags and a link to the market. Each market is announced once (`notified_at`); the message id is stored (`telegram_message_id`), so posted messages can be re-rendered from current data and edited silently (`edit_pending`, `make notify-reformat`); markets that closed or stopped being awesome before sending are dropped. After a rule change, `make markets-awesome` queues the messages of posted markets that are no longer awesome for deletion (`delete_pending`); the bot deletes them up to 100 per call and forgets the post, so a market that becomes awesome again is announced anew with a new message id. Telegram may refuse to delete messages older than 48 h: those stay in the channel (`telegram_delete_error`). Order of work: deletions, then new markets, then edits. Markets that were awesome before notifications existed are not announced.
5. **Admin and alerts.** Admins (`TELEGRAM_ADMIN_IDS`) can ask the bot in a private chat for `/status` and `/health_check`. Every minute the bot checks MongoDB and Telegram and evaluates alert rules; an admin gets one message when a problem starts (repeated every 6 h while it lasts) and one when it is resolved. Health data is kept in memory and counted since the bot started.
6. **Detectors** *(planned).* Only for awesome markets that are still open: sharp price jumps, edits of the question or description, closing and resolution, volume spikes.

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
> `make clean` deletes the MongoDB data volume: all loaded markets and the filter rules (`filter_rules` and its history) are lost — nothing is seeded back, market updates stay paused until a deny rule is added again. MongoDB users are recreated from the passwords currently in `.env`.

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
| `TELEGRAM_BOT_TOKEN` | — | Bot token from @BotFather; empty turns notifications off |
| `TELEGRAM_CHANNEL_ID` | — | Channel for notifications: `@name`, `name` or `-100…`; the bot must be a channel admin allowed to post |
| `TELEGRAM_ADMIN_IDS` | — | Telegram user ids (comma-separated) allowed to use admin commands and receiving alerts; empty turns them off |

An invalid `LOG_LEVEL` or `LOG_FORMAT`, a wrong Telegram token or an unknown channel stops the bot at startup. If Telegram is merely unreachable, the bot starts anyway: markets keep updating and notifications wait in the queue.

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
make notify-queue       # markets waiting for a Telegram notification, last sent
make notify-backfill    # queue all open awesome markets never announced (slow, ~20/min)
make notify-reformat    # re-render and edit all posted messages after a format change (slow)
make notify-edits-stop  # cancel queued edits; edited messages stay as they are
make notify-deletes-stop # cancel queued deletions; messages stay in the channel
make rules              # current filter rules
make rules-history      # last rule edits (n=50 for more)
```

#### Filter rules

The rules live only in MongoDB (`filter_rules`) — there are no defaults in the code. There are four lists: allow and deny, for tags and for words. Tags are Polymarket labels matched exactly (case-sensitive); words are stored lowercase. A value can be in only one list: adding it to the opposite list is an error, remove it first. Every edit is logged in `filter_rules_log` with the time and `$USER`:

```bash
make rule-add kind=tag action=deny value="Soccer"      # warns if no market has this tag (typo?)
make rule-add kind=word action=allow value="elon musk"
make rule-remove kind=tag value="Games"                # from whichever list holds it
```

Allow rules win over deny rules; a market that matches nothing is awesome. A word matches at the start of a word, in any case, in the question, event title, description and tags: `alien` finds "Aliens" but not "Valiente"; a phrase like `elon musk` works too.

The bot reads the rules every cycle, so a change applies to markets updated from then on; `make markets-awesome` recomputes all markets (and queues the ones that became awesome for the channel). With no deny rules every market would be awesome, so the bot pauses market updates (the admins get the "Market updates are failing" alert) and `make markets-awesome` refuses to run.

These commands read `MONGO_URI` from `.env` and run `mongosh` inside the `mongodb` container. Any query:

```bash
set -a; . ./.env; set +a
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.markets.find({tags: "Politics", closed: false}).limit(5)'
```

### Admin commands and alerts

1. Send `/start` to the bot in a private chat. The bot does not answer strangers, but logs their id: `make logs` → `Telegram /start from a non-admin … user_id=…`.
2. Put the id into `TELEGRAM_ADMIN_IDS` in `.env` and restart the bot (`make up`).

| Command | Shows |
|---|---|
| `/status` | Queues (to send, to edit); awesome markets (total, open); last 24 h: markets updated, new markets, became awesome, announced, updates from the API; last update cycle |
| `/health_check` | Uptime; MongoDB (live ping); updater and Telegram: last success, failures, last error; Telegram 429s; active alerts |

Alerts (🔴 when a problem starts, 🟢 when it is resolved):

| Problem | 🔴 when | 🟢 when |
|---|---|---|
| Market updates are failing | 3 cycles in a row failed | a cycle succeeds |
| Market updates are lagging | 3 cycles in a row took longer than `POLL_INTERVAL` | a cycle fits the interval |
| MongoDB is unreachable | 2 pings in a row failed (one a minute) | a ping succeeds |
| Telegram rate limit | a 429 with a penalty over 5 min | the penalty is over |
| Notification queue is growing | more than 500 markets wait | fewer than 100 |
| Telegram was unreachable | — (an alert cannot be delivered) | Telegram answers again: one message with the outage period |

Only one bot instance may receive messages at a time: running a second copy with the same token (e.g. `go run ./cmd/bot` next to Docker) makes Telegram answer `409 Conflict`.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, tests and checks. Run `make check` before opening a pull request.

## Security

Please do not report vulnerabilities in public issues. See [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © moonmouse11
