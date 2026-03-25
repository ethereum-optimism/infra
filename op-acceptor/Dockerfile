FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

WORKDIR /app

RUN apk add --no-cache make git

COPY op-acceptor/ .

RUN make build

FROM dhi.io/golang:1.26-alpine3.23-dev

RUN apk add --no-cache build-base git zstd

WORKDIR /app

RUN addgroup -S app && adduser -S app -G app && \
    mkdir -p /devnets && \
    chown -R app:app /devnets

RUN mkdir -p /go && \
    mkdir -p /go/pkg /go/bin /go/src && \
    chown -R app:app /go

COPY --from=builder /app/bin/op-acceptor /app/
COPY op-acceptor/example-validators.yaml /app/

RUN chown -R app:app /app/

RUN /app/op-acceptor --help && \
    go version && git --version && zstd --version

USER app

VOLUME /devnets

ENTRYPOINT ["/app/op-acceptor"]
