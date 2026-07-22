FROM golang:1.23-alpine3.20 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o bailiff ./cmd/bailiff

FROM alpine:3.20.3
RUN apk add --no-cache ca-certificates git bash openssh
COPY --from=builder /app/bailiff /
ENTRYPOINT ["/bailiff"]