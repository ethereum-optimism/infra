FROM chainguard/wolfi-base:latest AS builder

COPY ./peer-mgmt-service /app

WORKDIR /app
RUN apk add --no-cache go-1.26 make jq bash git build-base
RUN make build

FROM chainguard/wolfi-base:latest

WORKDIR /app

COPY --from=builder /app/bin/pms /app/

RUN /app/pms 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/app/pms"]
