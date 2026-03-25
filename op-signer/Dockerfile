FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

COPY ./op-signer /app

WORKDIR /app
RUN apk --no-cache add make jq bash git alpine-sdk
RUN make build

FROM dhi.io/alpine-base:3.23

WORKDIR /app

COPY --from=builder /app/bin/op-signer /app/

RUN ["/app/op-signer", "--help"]

ENTRYPOINT ["/app/op-signer"]
