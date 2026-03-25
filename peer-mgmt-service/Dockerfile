FROM golang:1.26.1-alpine3.22 as builder

COPY ./peer-mgmt-service /app

WORKDIR /app
RUN apk add --no-cache make jq bash git alpine-sdk
RUN make build

FROM alpine:3.22
RUN apk --no-cache add make jq bash git alpine-sdk redis

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

COPY --from=builder /app/bin/pms /app

RUN /app/pms 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/app/pms"]
