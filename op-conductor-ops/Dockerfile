FROM chainguard/wolfi-base:latest

RUN apk add --no-cache python-3.12 py3.12-poetry && \
    printf '#!/bin/sh\nexec python3.12 -m poetry "$@"\n' > /usr/bin/poetry && \
    chmod +x /usr/bin/poetry

WORKDIR /app

# Copy only the dependency files initially to leverage Docker caching
COPY /op-conductor-ops/pyproject.toml /op-conductor-ops/poetry.lock* ./

# Install dependencies into the system Python (no venv)
RUN python3.12 -m poetry config virtualenvs.create false \
 && python3.12 -m poetry install --no-interaction --no-ansi

# Copy the rest of the application
COPY /op-conductor-ops/ .

# Set permissions for the executable
RUN chmod +x op-conductor-ops

ENTRYPOINT ["./op-conductor-ops"]
