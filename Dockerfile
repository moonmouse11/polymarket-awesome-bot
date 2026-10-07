FROM golang:alpine AS builder

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN go build -o polymarket-bot ./cmd/bot

FROM alpine:latest

WORKDIR /root/

COPY --from=builder /app/polymarket-bot .

CMD ["./polymarket-bot"]
