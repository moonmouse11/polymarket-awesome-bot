# polymarket-awesome-bot

[![CI](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/moonmouse11/polymarket-awesome-bot/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/moonmouse11/polymarket-awesome-bot)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[English](README.md) | **Русский**

Бот для исследования рынков прогнозов [Polymarket](https://polymarket.com): находит *awesome* рынки — необычные, странные, заслуживающие внимания — и следит за их изменениями.

> [!NOTE]
> Проект в ранней разработке. Сейчас он загружает и хранит рынки; поиск awesome-рынков и уведомления запланированы. Каналы уведомлений — отдельная будущая часть, и Telegram будет лишь одним из них.

## Содержание

- [Статус](#статус)
- [Как это работает](#как-это-работает)
- [Начало работы](#начало-работы)
- [Конфигурация](#конфигурация)
- [Использование](#использование)
- [Данные](#данные)
- [Логи](#логи)
- [Структура проекта](#структура-проекта)
- [Заметки о Polymarket Gamma API](#заметки-о-polymarket-gamma-api)
- [Участие в разработке](#участие-в-разработке)
- [Безопасность](#безопасность)
- [Лицензия](#лицензия)

## Статус

| Часть | Состояние |
|---|---|
| Полная загрузка всех рынков (открытых и закрытых) в MongoDB | ✅ готово, отдельная команда `make markets-load` |
| Коллекция `keywords` с начальным засевом | ✅ готово |
| Health check (`/health`), корректная остановка | ✅ готово |
| Анализ по ключевым словам (`KeywordAnalyzer`) | 🚧 код и тесты есть, в бот пока не подключён |
| AI-анализатор Jev | 🚧 заглушка, ждёт ранний доступ к API |
| Ежеминутный опрос обновлений | 📋 запланировано |
| Детекторы изменений awesome-рынков | 📋 запланировано |
| Уведомления (Telegram и другие каналы) | 📋 запланировано |
| Переводы рынков (английский / русский) | 📋 запланировано |

## Как это работает

1. **Первичная загрузка.** Все рынки — сначала открытые, затем закрытые — забираются из Gamma API постранично и сохраняются в MongoDB. Запускается вручную: `make markets-load`.
2. **Обновления** *(запланировано).* Раз в минуту бот запрашивает рынки, отсортированные по `updatedAt`, и обновляет записи в базе. Предыдущая версия записи служит состоянием «было» для сравнения.
3. **Awesome-рынки** *(запланировано).* Рынок становится awesome, если в нём найдено слово из коллекции `keywords`.
4. **Детекторы** *(запланировано).* Только для awesome-рынков, которые ещё открыты: резкий скачок цены, правка вопроса или описания, закрытие и разрешение рынка, всплеск объёма.

## Начало работы

### Требования

- Docker и Docker Compose
- Go 1.27+ — только для `make markets-load` и запуска бота без Docker
- `make`

### Запуск

```bash
git clone https://github.com/moonmouse11/polymarket-awesome-bot.git
cd polymarket-awesome-bot
cp .env.example .env   # задайте пароли MongoDB, см. ниже
make up                # собрать и запустить бота + MongoDB
make logs              # логи бота
make down              # остановить
```

Проверить, что бот жив:

```bash
curl http://127.0.0.1:8080/health   # OK
```

`make help` показывает все команды.

> [!WARNING]
> `make clean` удаляет том с данными MongoDB: все загруженные рынки и правки ключевых слов пропадут. Пользователи MongoDB создадутся заново из паролей, которые в этот момент записаны в `.env`.

### Пользователи MongoDB

MongoDB запускается с проверкой пароля. Пользователи создаются **один раз**, при первом запуске на пустом томе (`docker/mongo-init/01-users.js`):

| Пользователь | Права | Кто использует |
|---|---|---|
| `root` (`MONGO_ROOT_*`) | всё | только администрирование |
| `polymarket_app` (`MONGO_APP_*`) | `readWrite` только на базу `polymarket` | бот |
| `polymarket_test` (`MONGO_TEST_*`) | владелец только базы `polymarket_test` | тесты |

- Без паролей в `.env` Docker Compose не запустится.
- Используйте только буквы и цифры — пароли подставляются в строку подключения как есть: `openssl rand -hex 24`.
- Если поменять пароль в `.env` позже, в базе он не изменится: смените его в MongoDB вручную или пересоздайте том.

Порты бота (`8080`) и MongoDB (`27017`) опубликованы только на `127.0.0.1` и из сети недоступны.

## Конфигурация

Бот настраивается переменными окружения. Docker Compose берёт их из `.env`; сам Go `.env` не читает.

| Переменная | По умолчанию | Назначение |
|---|---|---|
| `MONGO_URI` | `mongodb://localhost:27017` | Подключение бота к MongoDB под `polymarket_app`. Docker Compose собирает его из `MONGO_APP_*`; в `.env` нужен для `go run` |
| `MONGO_ROOT_USER` / `MONGO_ROOT_PASSWORD` | `root` / — | Администратор MongoDB (пароль обязателен) |
| `MONGO_APP_USER` / `MONGO_APP_PASSWORD` | `polymarket_app` / — | Пользователь бота (пароль обязателен) |
| `MONGO_TEST_USER` / `MONGO_TEST_PASSWORD` | `polymarket_test` / — | Пользователь тестов (пароль обязателен) |
| `MONGO_TEST_URI` | — | Подключение тестов `internal/db` |
| `PORT` | `8080` | Порт health check сервера |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` — для сбора логов, `console` — читаемый вывод для локальной работы |
| `JEV_API_KEY` | — | Зарезервировано под анализатор Jev, пока не используется |
| `TELEGRAM_BOT_TOKEN` | — | Зарезервировано под уведомления, пока не используется |

Неверное значение `LOG_LEVEL` или `LOG_FORMAT` не даёт боту стартовать.

## Использование

### Загрузка и просмотр рынков

```bash
make markets-load               # полная загрузка: открытые, потом закрытые (идёт долго)
make markets-count              # всего / закрытых / активных, время последней записи
make markets-sample             # 3 последних обновлённых рынка
make markets-find Q=alien       # поиск по тексту вопроса (регистр не важен), до 20 штук
make markets-show ID=559651     # полный документ рынка
```

Загрузка запускается на хосте, а не в Docker, и требует Go. Её можно запускать повторно: рынки хранятся по их id в Polymarket, поэтому повторная загрузка перезаписывает документы, а не создаёт дубликаты. Если загрузка оборвалась, уже записанные страницы остаются — просто запустите её ещё раз. Полная загрузка занимает около 1,8 ГБ на диске MongoDB.

Команды читают `MONGO_URI` из `.env` и запускают `mongosh` внутри контейнера `mongodb`. Произвольный запрос:

```bash
set -a; . ./.env; set +a
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.markets.find({tags: "Politics", closed: false}).limit(5)'
```

### Ключевые слова

При каждом старте бот создаёт уникальный индекс по `word` и засевает начальный список (`analyzer.DefaultKeywords`) **только если коллекция пустая**, поэтому правки в базе переживают перезапуски. Слова хранятся в нижнем регистре.

```bash
set -a; . ./.env; set +a
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.keywords.find({}, {_id: 0, word: 1})'
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.keywords.insertOne({word: "pope", created_at: new Date()})'
docker compose exec mongodb mongosh "$MONGO_URI" --eval 'db.keywords.deleteOne({word: "boxing"})'
```

## Данные

База `polymarket`:

| Коллекция | Что хранит |
|---|---|
| `markets` | Все рынки Polymarket. `_id` — id рынка в Polymarket. Индексы: `updated_at`, `closed`, `tags` |
| `keywords` | Слова, по которым рынок считается awesome: `{ word, created_at }`, уникальный индекс по `word` |
| `awesome_markets` | Зарезервировано под awesome-рынки (пока пустая) |

Какие поля рынка хранятся, задано в [`internal/polymarket/market.go`](internal/polymarket/market.go). Чтобы добавить поле из API, допишите его в `apiMarket`, `Market` и `toMarket`.

## Логи

Логи пишутся в stdout через [zerolog](https://github.com/rs/zerolog) в виде структурированных записей:

```json
{"level":"info","closed":false,"pages":10,"markets":1000,"time":"2026-10-08T12:00:00Z","message":"Loading markets"}
```

Логи бота: `make logs`. Логи загрузки рынков выводятся прямо в терминал:

| Уровень | Что пишется |
|---|---|
| `error` | Загрузка упала: какой проход, сколько страниц и рынков успели записать, причина |
| `warn` | Неудачный запрос к Polymarket перед повтором: `attempt`, `max_attempts`, `status` или `error`, `retry_in` |
| `info` | Начало и конец каждого прохода, прогресс каждые 10 страниц |
| `debug` | Каждая страница: `page`, `markets`, `took` |

Подробный режим: `LOG_LEVEL=debug make markets-load`. Курсор пагинации и `MONGO_URI` (в нём пароль) в логи не пишутся.

## Структура проекта

```
cmd/bot/              точка входа бота: конфигурация, MongoDB, keywords, health check
cmd/load-markets/     полная загрузка рынков (make markets-load)
internal/polymarket/  клиент Gamma API: /markets/keyset, повторы, модель Market
internal/db/          MongoDB: подключение, markets, keywords, awesome_markets
internal/analyzer/    анализаторы: keyword, jev (заглушка), hybrid (keyword → jev)
internal/logger/      настройка zerolog из LOG_LEVEL / LOG_FORMAT
docker/mongo-init/    пользователи MongoDB, создаются при первом запуске
```

## Заметки о Polymarket Gamma API

Проверено запросами к `https://gamma-api.polymarket.com` (октябрь 2026):

- API публичный, ключ не нужен.
- Рынков около 4,16 млн: ~266 тыс. открытых и ~3,89 млн закрытых.
- Без параметра `closed` возвращаются только открытые рынки, `closed=true` — только закрытые. Открытые и закрытые одним запросом не получить. Это относится и к выборке по id.
- `/markets` отдаёт максимум 100 рынков за запрос, а `offset` перестаёт работать после нескольких тысяч записей. Для полного обхода нужен `/markets/keyset` с `after_cursor`.
- `/markets?id=1&id=2&…` возвращает до 100 рынков по id; `/markets/{id}` — рынок в любом статусе.
- Поля `category` нет. Теги приходят с параметром `include_tag=true`.
- `outcomes` и `outcomePrices` — JSON-массивы, закодированные в строку: `"[\"0.007\", \"0.993\"]"`.
- Надёжный признак закрытия — `closed`. На `endDate` полагаться нельзя: рынок может закрыться раньше или остаться открытым после неё.
- `?locale=ru` (и другие языки) переводит `question`, `outcomes` и заголовок события, но не `description`. Переведены не все рынки.

## Участие в разработке

Помощь приветствуется. Как настроить окружение, запускать тесты и проверки — в [CONTRIBUTING.md](CONTRIBUTING.md). Перед открытием pull request запустите `make check`.

## Безопасность

Пожалуйста, не сообщайте об уязвимостях в публичных issue. Подробнее — в [SECURITY.md](SECURITY.md).

## Лицензия

[MIT](LICENSE) © moonmouse11
