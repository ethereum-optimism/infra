FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

COPY ./cci-stats /app

WORKDIR /app
RUN apk add --no-cache make jq bash git alpine-sdk
RUN make build

FROM dhi.io/alpine-base:3.23

WORKDIR /app

COPY --from=builder /app/bin/cci-stats /app/

ENTRYPOINT ["/app/cci-stats"]
