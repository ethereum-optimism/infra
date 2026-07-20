FROM chainguard/wolfi-base:latest AS builder

RUN apk add --no-cache nodejs-24 yarn

WORKDIR /opt/optimism/replica-healthcheck
COPY ./replica-healthcheck .

RUN yarn install --frozen-lockfile --ignore-scripts && yarn cache clean
RUN yarn build

FROM chainguard/wolfi-base:latest

RUN apk add --no-cache nodejs-24 npm

WORKDIR /opt/optimism/replica-healthcheck

COPY --from=builder /opt/optimism/replica-healthcheck /opt/optimism/replica-healthcheck

ENTRYPOINT ["npm", "run", "start"]
