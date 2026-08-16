FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/bot ./cmd/bot

FROM alpine:3.21
RUN adduser -D -u 10001 bot && apk add --no-cache ca-certificates
USER bot
COPY --from=build /out/bot /usr/local/bin/bot
ENTRYPOINT ["/usr/local/bin/bot"]
CMD ["-config", "/etc/gh-notify/config.toml"]
