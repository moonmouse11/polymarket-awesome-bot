FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN go build -o polymarket-bot ./cmd/bot

FROM alpine:latest

# Run as an unprivileged user instead of root.
RUN adduser -D -H -u 10001 app

WORKDIR /app

COPY --from=builder /app/polymarket-bot .

# Numeric UID: Kubernetes runAsNonRoot can only verify a numeric user.
USER 10001

CMD ["./polymarket-bot"]
