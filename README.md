# gh-notify-go

Telegram-бот, который доставляет события GitHub-репозиториев в групповые чаты
в реальном времени: `push`, `pull_request`, `issues`. Управление — через
inline-клавиатуры в личных сообщениях, единственная команда — `/start`.

Архитектура: один Go-бинарник. GitHub-webhook проверяет HMAC, дедуплицирует
по delivery id, находит подходящие интеграции и пишет строки в Postgres-outbox.
Пул воркеров разбирает outbox, рендерит событие в Telegram-HTML и отправляет
с ретраями и backoff.

## Переменные окружения

| Переменная | Назначение |
|---|---|
| `BOT_TOKEN` | токен бота от @BotFather |
| `DATABASE_URL` | строка вида `postgres://…` (SQLite не поддерживается) |
| `GITHUB_WEBHOOK_SECRET` | секрет GitHub App webhook |
| `SECRET_KEY` | ключ AES-GCM для шифрования токенов установок — 32 случайных байта в base64, сгенерировать: `openssl rand -base64 32` |

Остальные настройки — в `config.toml` (см. `config.example.toml`).

## Создание GitHub App

1. Открой https://github.com/settings/apps/new.
2. Webhook URL: `https://<host>/gh/webhook`, secret — значение
   `GITHUB_WEBHOOK_SECRET`.
3. Setup URL: `https://<host>/github/setup`.
4. Permissions → Repository: **Contents: read, Issues: read,
   Pull requests: read, Metadata: read**.
5. Subscribe to events: `push`, `pull_request`, `issues`.
6. Скачай приватный ключ `.pem` и положи его в `deploy/secrets/github-app.pem`
   (права `0600`).

## Запуск

```bash
cd deploy
export POSTGRES_PASSWORD=…
export BOT_TOKEN=…
export GITHUB_WEBHOOK_SECRET=…
export SECRET_KEY=$(openssl rand -base64 32)
docker compose up -d
```

Бот слушает `127.0.0.1:8080`; наружу его публикует обратный прокси
(пример — `deploy/Caddyfile.example`). Проверить живость: `GET /healthz`.

## Тесты

```bash
go test ./...
```

Тесты хранилища, outbox и сервиса поднимают одноразовый Postgres через
testcontainers — нужен работающий Docker.
