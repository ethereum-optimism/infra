FROM dhi.io/golang:1.26-alpine3.23-dev AS builder

COPY ./peer-mgmt-service /app

WORKDIR /app
RUN apk add --no-cache make jq bash git alpine-sdk
RUN make build

FROM dhi.io/alpine-base:3.23-dev

RUN apk --no-cache add make jq bash git alpine-sdk redis

RUN addgroup -S app && adduser -S app -G app
USER app
WORKDIR /app

COPY --from=builder /app/bin/pms /app/

RUN /app/pms 2>&1 || [ $? -lt 127 ]

ENTRYPOINT ["/app/pms"]
