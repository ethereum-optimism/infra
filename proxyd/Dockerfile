FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add make jq git gcc musl-dev linux-headers

COPY ./proxyd /app

WORKDIR /app

RUN make proxyd

FROM dhi.io/alpine-base:3.23

EXPOSE 8080

VOLUME /etc/proxyd

COPY --from=builder /app/bin/proxyd /bin/proxyd

RUN /bin/proxyd 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/bin/proxyd"]
CMD ["/etc/proxyd/proxyd.toml"]
