FROM chainguard/wolfi-base:latest AS builder

COPY ./op-conductor-mon /app

WORKDIR /app
RUN apk add --no-cache go-1.26 make jq bash git build-base
RUN CGO_ENABLED=0 make build

FROM chainguard/wolfi-base:latest
RUN apk add --no-cache 'ca-certificates-bundle>=20260909-r3'

COPY --from=builder /app/bin/op-conductor-mon /app/op-conductor-mon

ENTRYPOINT ["/app/op-conductor-mon"]
