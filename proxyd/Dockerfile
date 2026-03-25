FROM golang:1.26.1-alpine3.22 AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

COPY ./proxyd /app

WORKDIR /app

RUN make proxyd

FROM alpine:3.22

RUN apk add bind-tools jq curl bash git redis

COPY ./proxyd/entrypoint.sh /bin/entrypoint.sh

RUN apk update && \
    apk add ca-certificates && \
    chmod +x /bin/entrypoint.sh

EXPOSE 8080

VOLUME /etc/proxyd

COPY --from=builder /app/bin/proxyd /bin/proxyd

RUN /bin/proxyd 2>&1 || [ $? -lt 127 ] && \
    bash --version > /dev/null && jq --version > /dev/null && \
    curl --version > /dev/null && dig -v 2>&1 > /dev/null && \
    redis-cli --version > /dev/null

ENTRYPOINT ["/bin/entrypoint.sh"]
CMD ["/bin/proxyd", "/etc/proxyd/proxyd.toml"]
