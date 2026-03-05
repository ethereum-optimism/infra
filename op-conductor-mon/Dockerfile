FROM dhi.io/golang:1.24-dev AS builder

COPY ./op-conductor-mon /app

WORKDIR /app
RUN CGO_ENABLED=0 make build

FROM dhi.io/alpine-base:3.22

COPY --from=builder /app/bin/op-conductor-mon /app/op-conductor-mon

ENTRYPOINT ["/app/op-conductor-mon"]
