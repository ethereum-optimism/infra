FROM chainguard/wolfi-base:latest AS builder

COPY ./op-signer /app

WORKDIR /app
RUN apk --no-cache add go-1.26 make jq bash git build-base
RUN make build

FROM chainguard/wolfi-base:latest

WORKDIR /app

COPY --from=builder /app/bin/op-signer /app/

RUN ["/app/op-signer", "--help"]

ENTRYPOINT ["/app/op-signer"]
