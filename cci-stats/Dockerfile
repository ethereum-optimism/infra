FROM golang:1.26.1-alpine3.22 as builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

COPY ./cci-stats /app

WORKDIR /app
RUN apk add --no-cache make jq bash git alpine-sdk
RUN make build

FROM alpine:3.22
RUN addgroup -S app && adduser -S app -G app
RUN apk add --no-cache ca-certificates
USER app
WORKDIR /app

COPY --from=builder /app/bin/cci-stats /app

RUN /app/cci-stats 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/app/cci-stats"]
