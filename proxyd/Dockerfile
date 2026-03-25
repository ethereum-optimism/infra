FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

COPY ./proxyd /app

WORKDIR /app

RUN make proxyd

FROM dhi.io/alpine-base:3.23-dev

RUN apk add --no-cache bind-tools jq curl bash git redis

COPY ./proxyd/entrypoint.sh /bin/entrypoint.sh

RUN chmod +x /bin/entrypoint.sh

EXPOSE 8080

VOLUME /etc/proxyd

COPY --from=builder /app/bin/proxyd /bin/proxyd

RUN /bin/proxyd 2>&1 || [ $? -lt 127 ] && \
    bash --version > /dev/null && jq --version > /dev/null && \
    curl --version > /dev/null && dig -v 2>&1 > /dev/null && \
    redis-cli --version > /dev/null

ENTRYPOINT ["/bin/entrypoint.sh"]
CMD ["/bin/proxyd", "/etc/proxyd/proxyd.toml"]
