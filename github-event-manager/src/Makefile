.PHONY: help venv install clean gen-requirements test build-docker scan-code scan-deps security-scan

# Python version
PYTHON = python3
VENV = .venv
SHELL := /bin/bash
PROJECT_ROOT = ..

# Get OS type
ifeq ($(OS),Windows_NT)
	VENV_BIN = $(VENV)/Scripts
else
	VENV_BIN = $(VENV)/bin
endif

# Define the test command
TEST_CMD = -m unittest discover -s

# Define the files to search for tests, ignoring hidden folders and __ folders
TEST_FILES = $(shell find . -type f -name 'test_*.py' ! -path '*/\.*' ! -name '__*')

help:
	@echo "Available commands:"
	@echo "  make venv            - Create virtual environment if not exists"
	@echo "  make install         - Install dependencies from Pipfile"
	@echo "  make clean           - Remove virtual environment"
	@echo "  make test            - Run tests in all subdirectories"
	@echo "  make build-docker    - Build the Docker image (from project root)"
	@echo "  make scan-code       - Run static code security analysis (bandit)"
	@echo "  make scan-deps       - Scan dependencies for vulnerabilities (OWASP Dependency-Check)"
	@echo "  make security-scan   - Run all security scans"
	@echo "  Activate virtual environment with: source $(VENV_BIN)/activate"

# Create fresh virtual environment
venv:
	@echo "Creating fresh virtual environment..."
	@rm -rf $(VENV)
	@python3 -m venv $(VENV)
	@echo "Virtual environment created. Activate with: source $(VENV_BIN)/activate"

# Install dependencies directly with pip, bypassing pipenv
install: venv
	@echo "Installing dependencies..."
	@. $(VENV_BIN)/activate && pip install --upgrade pip
	@if [ -f "Pipfile" ]; then \
		echo "Installing pipenv and dependencies from Pipfile..."; \
		. $(VENV_BIN)/activate && pip install pipenv && \
		pipenv install && \
		pipenv lock; \
	elif [ -f "requirements.txt" ]; then \
		echo "Installing dependencies from requirements.txt..."; \
		. $(VENV_BIN)/activate && pip install -r requirements.txt; \
	else \
		echo "No Pipfile or requirements.txt found"; \
	fi

clean:
	@echo "Removing virtual environment..."
	@rm -rf $(VENV)
	@find . -type d -name "__pycache__" -exec rm -rf {} +
	@find . -type f -name "*.pyc" -deletes

# Default target
.DEFAULT_GOAL := help

# Run tests in all subdirectories
test: 
	@for file in $(TEST_FILES); do \
		echo "Running tests in $$file..."; \
		$(VENV_BIN)/python -m unittest $$file; \
	done

# Target to build the docker image
build-docker:
	@echo "Building Docker image for linux/amd64: github-event-manager:latest..."
	@echo "Running from project root: $(PROJECT_ROOT)"
	@cd $(PROJECT_ROOT) && docker build --progress=plain --platform linux/amd64 -t github-event-manager:latest .
	@echo "Docker build complete."

# Run bandit code security analysis
scan-code: venv
	@echo "Running code security scan (bandit)..."
	# Ensure bandit is installed if requirements changed
	@$(VENV_BIN)/pip show bandit > /dev/null || $(VENV_BIN)/pip install bandit
	@$(VENV_BIN)/bandit -r . --exclude ./$(VENV) -ll -ii

# Run OWASP Dependency-Check vulnerability scan
scan-deps: venv
	@echo "Scanning dependencies for known vulnerabilities (OWASP Dependency-Check)..."
	@if ! command -v dependency-check.sh &> /dev/null; then \
		echo "Error: dependency-check.sh command not found."; \
		echo "Please install OWASP Dependency-Check CLI (https://owasp.org/www-project-dependency-check/#div-installation)"; \
		exit 1; \
	fi
	# Generate combined requirements file (more reliable for dep-check)
	@echo "Generating combined requirements file..."
	@echo "Running OWASP Dependency-Check..."
	# Scan the project - pointing it to the source might help find requirements files
	# Alternatively, explicitly scan the requirements file: --scan requirements.txt
	@dependency-check.sh \
		--project "GitHubEventManager" \
		--scan . \
		--format "HTML" --format "JSON" \
		--out .dependency-check-reports \
		--failOnCVSS 7 \
		--enableExperimental # Necessary for Python scanning
		--scan requirements.txt
		--scan project_requirements.txt
	@echo "Dependency-Check scan complete. Reports in .dependency-check-reports/"
	@rm combined_requirements.txt # Clean up temp file

# Run all security scans
security-scan: scan-code scan-deps
	@echo "Security scans complete."

