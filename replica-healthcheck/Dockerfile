FROM chainguard/wolfi-base:latest AS builder

RUN apk add --no-cache nodejs-24 yarn

WORKDIR /opt/optimism/replica-healthcheck
COPY ./replica-healthcheck .

RUN yarn install --frozen-lockfile --ignore-scripts && yarn cache clean
RUN yarn build
RUN yarn install --frozen-lockfile --ignore-scripts --production && yarn cache clean

FROM chainguard/wolfi-base:latest

RUN apk add --no-cache 'ca-certificates-bundle>=20260909-r3' nodejs-24

WORKDIR /opt/optimism/replica-healthcheck

COPY --from=builder /opt/optimism/replica-healthcheck/dist ./dist
COPY --from=builder /opt/optimism/replica-healthcheck/node_modules ./node_modules

ENTRYPOINT ["node", "dist/src/service.js"]
