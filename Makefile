.PHONY: test lint build sqlc migrate-new

test:
	go test ./...

build:
	go build -o bin/bot ./cmd/bot

sqlc:
	sqlc generate

migrate-new:
	goose -dir internal/storage/migrations create $(NAME) sql
