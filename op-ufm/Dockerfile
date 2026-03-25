FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

COPY ./op-ufm /app

WORKDIR /app

RUN make ufm

FROM dhi.io/alpine-base:3.23-dev

COPY --from=builder /app/entrypoint.sh /bin/entrypoint.sh
COPY --from=builder /app/bin/ufm /bin/ufm

RUN chmod +x /bin/entrypoint.sh

RUN apk add --no-cache jq curl bind-tools

VOLUME /etc/ufm

EXPOSE 8080

RUN /bin/ufm 2>&1 || [ $? -lt 127 ] && \
    test -x /bin/entrypoint.sh && \
    jq --version > /dev/null && curl --version > /dev/null && \
    dig -v 2>&1 > /dev/null

ENTRYPOINT ["/bin/entrypoint.sh"]
CMD ["/bin/ufm", "/etc/ufm/config.toml"]
