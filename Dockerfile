# syntax=docker/dockerfile:1

# Stage 1: Build dependencies
# Uses the -dev variant which includes apk, shell, and build tools.
FROM dhi.io/python:3.12-alpine3.23-dev AS builder

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PATH="/app/venv/bin:$PATH"

RUN python -m venv /app/venv
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Stage 2: Runtime image
# The non-dev variant has NO shell, NO package manager, and runs as 'nonroot'.
FROM dhi.io/python:3.12-alpine3.23 AS runtime

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PATH="/app/venv/bin:$PATH"

# Copy virtual environment with all installed packages
COPY --from=builder /app/venv /app/venv

# Copy application code and set ownership to the runtime user
COPY --chown=nonroot:nonroot . /app

# Use /tmp for cache since nonroot can write there without permission issues.
ENV CACHE_DIR=/tmp/podimo-cache

USER nonroot

EXPOSE 12104

ENTRYPOINT ["python", "main.py"]
