# Exact versions: rebuilds must produce the same image. Bump deliberately.
FROM golang:1.27.2-alpine3.24 AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN go build -o polymarket-bot ./cmd/bot

FROM alpine:3.24.2

# Run as an unprivileged user instead of root.
RUN adduser -D -H -u 10001 app

WORKDIR /app

COPY --from=builder /app/polymarket-bot .

# Numeric UID: Kubernetes runAsNonRoot can only verify a numeric user.
USER 10001

CMD ["./polymarket-bot"]
