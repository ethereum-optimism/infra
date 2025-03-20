FROM python:3.12-slim

# Set working directory
WORKDIR /app

# Install system dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    build-essential \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Install Poetry
RUN curl -sSL https://install.python-poetry.org | python3 -
ENV PATH="/root/.local/bin:$PATH"

# Copy only the dependency files initially to leverage Docker caching
COPY /op-conductor-ops/pyproject.toml /op-conductor-ops/poetry.lock* ./

# Configure Poetry to not use virtualenvs inside the container
RUN poetry config virtualenvs.create false

# Install dependencies
RUN poetry install --no-interaction --no-ansi

# Copy the rest of the application
COPY /op-conductor-ops/ .

# Set permissions for the executable
RUN chmod +x op-conductor-ops

# Set the entrypoint to use the CLI
ENTRYPOINT ["./op-conductor-ops"]
