FROM golang:1.21.3-alpine3.18 as builder

COPY ./op-txproxy /app

WORKDIR /app
RUN apk --no-cache add make jq bash git alpine-sdk
RUN make build

FROM alpine:3.18
RUN apk --no-cache add ca-certificates

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

COPY --from=builder /app/bin/op-txproxy /app

ENTRYPOINT ["/app/op-txproxy"]
