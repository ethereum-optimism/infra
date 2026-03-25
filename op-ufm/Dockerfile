FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

COPY ./op-ufm /app

WORKDIR /app

RUN make ufm

FROM dhi.io/alpine-base:3.23

VOLUME /etc/ufm

EXPOSE 8080

COPY --from=builder /app/bin/ufm /bin/ufm

RUN /bin/ufm 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/bin/ufm"]
CMD ["/etc/ufm/config.toml"]
