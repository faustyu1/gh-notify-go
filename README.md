# gh-notify-go

Telegram-бот, который доставляет события GitHub-репозиториев в групповые чаты
в реальном времени. Управление — через inline-клавиатуры в личных сообщениях,
единственная команда — `/start`.

Архитектура: один Go-бинарник. GitHub-webhook проверяет HMAC, дедуплицирует
по delivery id, находит подходящие интеграции и пишет строки в Postgres-outbox.
Пул воркеров разбирает outbox, рендерит событие в Telegram-HTML и отправляет
с ретраями и backoff.

## События

19 типов: `push`, `pull_request`, `pull_request_review`,
`pull_request_review_comment`, `issues`, `issue_comment`, `commit_comment`,
`release`, `star`, `fork`, `create`, `delete`, `gollum`, `member`, `public`,
`deployment`, `deployment_status`, `check_suite`, `workflow_run`.

Каждый тип включается и выключается на интеграцию, плюс три пресета: всё,
только важное, ничего. Фильтры — по автору, ветке, метке и действию.
`star` приходит один раз на пользователя и репозиторий, навсегда. Когда чат
упирается в лимит `CHAT_PER_MINUTE`, лишнее сворачивается в дайджест.

## Права в чате

Подключать репозиторий к чату и менять его настройки может только
администратор этого чата — права спрашиваются у Telegram в момент действия
(кэш 1 минута), а не в момент отрисовки экрана. Это касается и просмотра:
экран чата, экраны событий, фильтров и здоровья закрыты той же проверкой.

## Переменные окружения

| Переменная | Назначение |
|---|---|
| `BOT_TOKEN` | токен бота от @BotFather |
| `BOT_USERNAME` | username бота без `@` |
| `DATABASE_URL` | строка вида `postgres://…` (SQLite не поддерживается) |
| `GITHUB_APP_ID`, `GITHUB_SLUG` | id и slug GitHub App |
| `GITHUB_PRIVATE_KEY_PATH` | путь к `.pem` приватному ключу App |
| `GITHUB_WEBHOOK_SECRET` | секрет GitHub App webhook |
| `PUBLIC_URL` | внешний адрес, на который смотрит обратный прокси |
| `SECRET_KEY` | ключ AES-GCM для шифрования токенов установок — 32 случайных байта в base64, сгенерировать: `openssl rand -base64 32` |
| `CHAT_PER_MINUTE`, `WORKERS` | тюнинг темпа доставки и числа воркеров |

Весь конфиг — переменные окружения. Список и значения по умолчанию — в
`.env.example`: скопируй в `.env`, бинарник прочитает его при старте.
Переменные настоящего окружения всегда важнее значений из файла.

## Создание GitHub App

1. Открой https://github.com/settings/apps/new.
2. Webhook URL: `https://<host>/gh/webhook`, secret — значение
   `GITHUB_WEBHOOK_SECRET`.
3. Setup URL: `https://<host>/github/setup`, галочка «Redirect on update» —
   включена.
4. Permissions → Repository: **Metadata: read, Contents: read, Issues: read,
   Pull requests: read, Deployments: read, Checks: read, Actions: read**.
5. Subscribe to events: те из списка выше, которые нужны; как минимум
   `push`, `pull_request`, `issues`, `installation`.
6. Скачай приватный ключ `.pem` и положи его в `deploy/secrets/github-app.pem`
   (права `0600`).

Ссылка на установку выдаётся ботом и несёт одноразовый `state`-токен с часовым
сроком жизни: он и определяет, чья это установка. Установка привязывается к
пользователю один раз — сменить владельца можно только удалив и поставив App
заново.

Для публичного App у GitHub обязательны homepage URL и ссылка на политику
конфиденциальности — см. [docs/PRIVACY.md](docs/PRIVACY.md), опубликуй её и
укажи в настройках App. У @BotFather — описание, about и картинка; команда
одна, `/start`.

## Запуск

```bash
cd deploy
cp .env.example .env   # заполнить DATABASE_URL, BOT_TOKEN, …
docker compose up -d
```

База по умолчанию внешняя: `DATABASE_URL` указывает на твой кластер,
`sslmode=require`, если он доступен не только по приватной сети. Схема
накатывается сама при старте, так что пользователю нужны права на DDL в этой
базе. Постгрес в комплекте — опция для автономного развёртывания:

```bash
docker compose -f docker-compose.yml -f docker-compose.local-db.yml up -d
```

Бот слушает `127.0.0.1:8080`; наружу его публикует обратный прокси
(пример — `deploy/Caddyfile.example`). `GET /healthz` пингует Postgres и
отвечает 503, если база недоступна, — на нём же висит healthcheck контейнера.

**Инстанс должен быть один.** Апдейты Telegram забираются long polling, две
копии начнут драться за `getUpdates`. Миграции при старте берут advisory-lock,
так что одновременный рестарт базу не поломает, но горизонтально
масштабировать процесс нельзя.

## Языки

Весь пользовательский текст вынесен в `internal/i18n/locales/*.yaml` и вшит
в бинарник. Пять локалей: английская (базовая, fallback), русская,
испанская, немецкая, португальская.

Язык личного интерфейса берётся из `language_code` Telegram при первом
`/start` и меняется кнопками на экране «Настройки». Язык уведомлений в чате —
язык владельца интеграции, поэтому две интеграции в одном чате могут говорить
на разных языках. Тест в `internal/i18n` следит, что во всех локалях один
и тот же набор ключей и плейсхолдеров.

## Тесты

```bash
make test        # один одноразовый Postgres-контейнер на весь прогон
make test-direct # вариант без Makefile: testcontainers сам поднимает Postgres
```

Каждый тест получает отдельную базу внутри общего контейнера, поэтому
`make test` стартует ровно один Postgres, а не по контейнеру на тест.
