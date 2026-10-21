FROM chainguard/wolfi-base:latest AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

COPY ./cci-stats /app

WORKDIR /app
RUN apk add --no-cache go-1.26 make jq bash git build-base
RUN make build

FROM chainguard/wolfi-base:latest
RUN apk add --no-cache 'ca-certificates-bundle>=20260909-r3'

WORKDIR /app

COPY --from=builder /app/bin/cci-stats /app/

ENTRYPOINT ["/app/cci-stats"]
