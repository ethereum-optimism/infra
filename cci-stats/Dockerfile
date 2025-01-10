FROM golang:1.23.4-alpine3.20 as builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

COPY ./cci-stats /app

WORKDIR /app
RUN apk add --no-cache make jq bash git alpine-sdk
RUN make build

FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app
RUN apk add --no-cache ca-certificates
USER app
WORKDIR /app

COPY --from=builder /app/bin/cci-stats /app

ENTRYPOINT ["/app/cci-stats"]
