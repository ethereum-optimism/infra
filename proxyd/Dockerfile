ARG BUILDER_IMAGE=golang:1.26.1-alpine3.23
ARG RUNTIME_IMAGE=alpine:3.23

FROM ${BUILDER_IMAGE} AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

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
