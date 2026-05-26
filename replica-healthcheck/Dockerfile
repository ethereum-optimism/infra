FROM node:23-alpine3.20 AS builder

WORKDIR /opt/optimism/replica-healthcheck
COPY ./replica-healthcheck .

RUN yarn install --frozen-lockfile && yarn cache clean
RUN yarn build

FROM dhi.io/node:22-alpine3.23-dev

WORKDIR /opt/optimism/replica-healthcheck

COPY --from=builder /opt/optimism/replica-healthcheck /opt/optimism/replica-healthcheck

ENTRYPOINT ["npm", "run", "start"]
