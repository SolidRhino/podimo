# syntax=docker/dockerfile:1

# Stage 1: Build dependencies
# Uses the -dev variant which includes apk, shell, and build tools
FROM dhi.io/python:3.12-alpine3.23-dev AS builder

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PATH="/app/venv/bin:$PATH"

# Build tools needed to compile lxml (feedgen dep) and other native modules.
# Also install runtime libraries so we can copy their .so files to the runtime stage.
RUN apk add --no-cache \
    libxml2-dev libxslt-dev \
    libxml2 libxslt \
    gcc libc-dev musl-dev

# Create virtual environment and install dependencies
RUN python -m venv /app/venv
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Discover which system libraries lxml links against so we can copy them.
# lxml's compiled extension is inside the venv; we find its .so and list
# its dynamic dependencies via ldd, then copy those libs to a staging dir.
RUN LXML_SO=$(find /app/venv -name 'etree*.so' -path '*/lxml/*' | head -1) && \
    mkdir -p /app/staged-libs && \
    if [ -n "$LXML_SO" ]; then \
        for lib in $(ldd "$LXML_SO" 2>/dev/null | awk '{print $3}' | grep -E '^/lib|^/usr/lib' | sort -u); do \
            [ -f "$lib" ] && cp "$lib" /app/staged-libs/ 2>/dev/null || true; \
        done; \
    fi && \
    ls -la /app/staged-libs/ || echo "No staged libs needed"

# Stage 2: Runtime image
# Uses the non-dev variant: no shell, no package manager, runs as nonroot.
FROM dhi.io/python:3.12-alpine3.23 AS runtime

WORKDIR /app

ENV PYTHONDONTWRITEBYTECODE=1
ENV PYTHONUNBUFFERED=1
ENV PATH="/app/venv/bin:$PATH"

# Copy virtual environment with all installed packages
COPY --from=builder /app/venv /app/venv

# Copy system libraries needed by compiled extensions (e.g. lxml).
# Staged from builder; if none are needed, this step is a no-op.
COPY --from=builder /app/staged-libs /tmp/staged-libs
RUN mkdir -p /lib && \
    for f in /tmp/staged-libs/*; do [ -f "$f" ] && cp "$f" /lib/; done 2>/dev/null || true

# Copy application code
COPY . /app

# Ensure cache directory is writable by the nonroot user.
# The runtime image runs as 'nonroot' by default.
RUN mkdir -p /app/cache && chown -R nonroot:nonroot /app

USER nonroot

EXPOSE 12104

ENTRYPOINT ["python", "main.py"]
