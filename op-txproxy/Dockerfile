FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

COPY ./op-txproxy /app
WORKDIR /app

RUN apk --no-cache add make jq bash git alpine-sdk
RUN make build

FROM dhi.io/alpine-base:3.23

COPY --from=builder /app/bin/op-txproxy /bin/op-txproxy

RUN ["/bin/op-txproxy", "--help"]

ENTRYPOINT ["/bin/op-txproxy"]
