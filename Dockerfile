# Stage 1: Build dependencies
FROM python:3.10-alpine AS builder

WORKDIR /src

# Build tools needed to compile lxml (feedgen dep) and other native modules
RUN apk add --no-cache libxml2-dev libxslt-dev gcc libc-dev musl-dev

COPY requirements.txt .
RUN pip install --no-cache-dir --user -r requirements.txt

# Stage 2: Runtime image
FROM python:3.10-alpine AS runtime

WORKDIR /src

# Copy installed packages from builder
COPY --from=builder /root/.local /home/podimo/.local

# Copy application code
COPY . /src

# Create non-root user
RUN addgroup -S podimo && adduser -S podimo -G podimo \
    && mkdir -p /src/cache \
    && chown -R podimo:podimo /src

USER podimo

# Ensure python can find user-installed packages
ENV PATH=/home/podimo/.local/bin:$PATH \
    PYTHONPATH=/home/podimo/.local/lib/python3.10/site-packages:$PYTHONPATH \
    PYTHONUNBUFFERED=1

EXPOSE 12104

ENTRYPOINT ["python3", "main.py"]
