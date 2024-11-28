FROM golang:1.22-alpine as builder

COPY ./op-txproxy /app
WORKDIR /app

RUN apk --no-cache add make jq bash git alpine-sdk
RUN make build

FROM alpine:3.18
RUN apk --no-cache add ca-certificates

COPY --from=builder /app/bin/op-txproxy /bin/op-txproxy

ENTRYPOINT ["/bin/op-txproxy"]
CMD ["/bin/op-txproxy"]
