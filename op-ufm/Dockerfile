FROM chainguard/wolfi-base:latest AS builder

ARG GITCOMMIT=docker
ARG GITDATE=docker
ARG GITVERSION=docker

RUN apk add --no-cache go-1.26 make jq git build-base

COPY ./op-ufm /app

WORKDIR /app

RUN make ufm

FROM chainguard/wolfi-base:latest

VOLUME /etc/ufm

EXPOSE 8080

COPY --from=builder /app/bin/ufm /bin/ufm

RUN /bin/ufm 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/bin/ufm"]
CMD ["/etc/ufm/config.toml"]
