FROM golang:1.23.8 AS builder

COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN go build -o /op-spamoor .

FROM ethpandaops/spamoor:latest

COPY --from=builder /op-spamoor .

ENTRYPOINT ["./op-spamoor"]
