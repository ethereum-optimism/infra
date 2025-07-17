FROM golang:1.23.8 AS builder

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -o /op-spamoor .

FROM joshklop0/spamoor:latest

COPY --from=builder /op-spamoor .

ENTRYPOINT ["./op-spamoor"]
