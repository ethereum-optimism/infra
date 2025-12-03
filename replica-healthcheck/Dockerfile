FROM node:23-alpine3.20

WORKDIR /opt/optimism/replica-healthcheck
COPY ./replica-healthcheck .

RUN yarn install --frozen-lockfile && yarn cache clean
RUN yarn build

ENTRYPOINT ["npm", "run", "start"]
