FROM chainguard/wolfi-base:latest

RUN apk add --no-cache python-3.12 py3.12-poetry && \
    printf '#!/bin/sh\nexec python3.12 -m poetry "$@"\n' > /usr/bin/poetry && \
    chmod +x /usr/bin/poetry

WORKDIR /app

COPY /op-conductor-ops/pyproject.toml /op-conductor-ops/poetry.lock* /op-conductor-ops/README.md ./
COPY /op-conductor-ops/op_conductor_ops/ ./op_conductor_ops/

RUN python3.12 -m poetry config virtualenvs.create false \
 && python3.12 -m poetry install --no-interaction --no-ansi

ENTRYPOINT ["op-conductor-ops"]
