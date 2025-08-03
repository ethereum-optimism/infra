FROM python:3.9-slim

WORKDIR /app

# Copy only requirements first to leverage Docker cache
COPY src/Pipfile .
COPY src/Pipfile.lock .
RUN pip install pipenv && pipenv sync --system

# Copy the rest of the application code from src
COPY src/ .

# Expose the default main application port and metrics port
# The actual mapping is done in the 'docker run' command
EXPOSE 5000
EXPOSE 9090

# Command to run the application using Gunicorn
# Reads configuration like ports and log level from environment variables set during 'docker run'
# Assumes main.py and the 'lib' directory are directly in /app (copied from src/)
CMD ["gunicorn", "--bind", "0.0.0.0:5000", "main:app", "--log-level", "info", "--workers", "1", "--timeout", "120"]