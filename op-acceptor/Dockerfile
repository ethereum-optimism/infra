FROM golang:1.23.5-alpine3.21 AS builder

WORKDIR /app

# Install dependencies first for better caching
RUN apk add --no-cache just

COPY op-acceptor/ .

RUN just build

FROM alpine:3.21

# Install Go binary
# (we copy Go directly from the builder stage for simplicity and consistency)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
ENV GOPATH="/go"
ENV PATH="${GOPATH}/bin:${PATH}"

WORKDIR /app
RUN addgroup -S app && adduser -S app -G app && \
    mkdir -p /devnets && \
    chown -R app:app /devnets

# Modify ownership of /go to app user (as we need write permissions)
RUN mkdir -p /go && \
mkdir -p /go/pkg /go/bin /go/src && \
chown -R app:app /go

COPY --from=builder /app/bin/op-acceptor /app/
COPY op-acceptor/example-validators.yaml /app/
USER app

VOLUME /devnets
ENTRYPOINT ["/app/op-acceptor"]
