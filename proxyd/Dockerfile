ARG BUILDER_IMAGE=chainguard/wolfi-base:latest
ARG RUNTIME_IMAGE=chainguard/wolfi-base:latest

FROM ${BUILDER_IMAGE} AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add --no-cache go-1.26 make jq git build-base

COPY ./proxyd /app

WORKDIR /app

RUN make proxyd

FROM ${RUNTIME_IMAGE}

EXPOSE 8080

VOLUME /etc/proxyd

COPY --from=builder /app/bin/proxyd /bin/proxyd

RUN /bin/proxyd 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/bin/proxyd"]
CMD ["/etc/proxyd/proxyd.toml"]
