FROM chainguard/wolfi-base:latest@sha256:003627df3c1e1bba0c4116afcddb314aca9594ee2328c7e876a8081a6c988b2e AS builder
RUN apk add --no-cache go-1.26
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o bailiff ./cmd/bailiff

FROM chainguard/wolfi-base:latest@sha256:003627df3c1e1bba0c4116afcddb314aca9594ee2328c7e876a8081a6c988b2e
RUN apk add --no-cache ca-certificates-bundle git bash openssh-client
COPY --from=builder /app/bailiff /
ENTRYPOINT ["/bailiff"]