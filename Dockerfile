# Exact versions: rebuilds must produce the same image. Bump deliberately.
FROM golang:1.27.2-alpine3.24 AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

# Every command, not only the bot: the server has no Go toolchain, so the
# make targets run them in this image (docker compose run --rm bot ./rules).
RUN go build -o bin/ ./cmd/...

FROM alpine:3.24.3

# Run as an unprivileged user instead of root.
RUN adduser -D -H -u 10001 app

WORKDIR /app

COPY --from=builder /app/bin/ .

# Numeric UID: Kubernetes runAsNonRoot can only verify a numeric user.
USER 10001

CMD ["./bot"]
