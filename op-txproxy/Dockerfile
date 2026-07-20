FROM chainguard/wolfi-base:latest AS builder

COPY ./op-txproxy /app
WORKDIR /app

RUN apk --no-cache add go-1.26 make jq bash git build-base
RUN make build

FROM chainguard/wolfi-base:latest

COPY --from=builder /app/bin/op-txproxy /bin/op-txproxy

RUN ["/bin/op-txproxy", "--help"]

ENTRYPOINT ["/bin/op-txproxy"]
