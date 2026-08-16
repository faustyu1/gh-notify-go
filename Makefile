.PHONY: test test-direct lint build sqlc migrate-new

# One throwaway Postgres for the whole suite: every test gets its own
# database inside it, so nothing is booted per test or per package.
test:
	@docker run -d --rm --name gh-notify-test-pg -p 55432:5432 \
		-e POSTGRES_USER=gh -e POSTGRES_PASSWORD=gh -e POSTGRES_DB=ghnotify \
		postgres:17-alpine >/dev/null || { echo 'docker run failed'; exit 1; }; \
	trap 'docker rm -f gh-notify-test-pg >/dev/null 2>&1' EXIT; \
	until docker exec gh-notify-test-pg pg_isready -U gh -d ghnotify >/dev/null 2>&1; do sleep 0.5; done; \
	TEST_DATABASE_URL='postgres://gh:gh@localhost:55432/ghnotify?sslmode=disable' \
		go test ./...

# Runs the suite the portable way: testcontainers boots one Postgres per
# test binary. Works in IDEs where the Makefile target is not available.
test-direct:
	go test ./...

build:
	go build -o bin/bot ./cmd/bot

sqlc:
	sqlc generate

migrate-new:
	goose -dir internal/storage/migrations create $(NAME) sql
