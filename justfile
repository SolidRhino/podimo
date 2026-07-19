set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

go_cmd := "go"
binary := "podimo-rss"
docker_image := "podimo-rss"
docker_container := "podimo-rss"

# List available recipes
default:
    @just --list

# --- Development targets ---

# Build the Go binary
build:
    {{go_cmd}} build -o {{binary}} .

# Run the Go test suite with race detection
test:
    {{go_cmd}} test -race ./... -v

# Build and run the server locally (uses ./config.yaml if found)
run: build
    #!/usr/bin/env bash
    if test -f "config.yaml"; then
        ./{{binary}} --config=config.yaml
    else
        ./{{binary}}
    fi

# Run with a custom config file path, e.g. `just run-config /etc/podimo-rss/config.yaml`
run-config configfile: build
    ./{{binary}} --config={{configfile}}

# Run go vet and gofmt
lint:
    {{go_cmd}} vet ./...
    test -z "$(gofmt -l .)" || { echo "Files need gofmt:"; gofmt -l .; exit 1; }

# Format Go source files
format:
    {{go_cmd}} fmt ./...

# Minify CSS and generate source maps. Originals (style.css, fonts.css)
# are kept as edit sources; templates link the .min.css versions.
css-minify:
    npx -y lightningcss-cli --minify --sourcemap static/style.css -o static/style.min.css
    npx -y lightningcss-cli --minify --sourcemap static/fonts.css -o static/fonts.min.css
    # sourceMappingURL is emitted as an absolute path; fix to relative.
    perl -pi -e 's|sourceMappingURL=static/|sourceMappingURL=|' static/style.min.css
    perl -pi -e 's|sourceMappingURL=static/|sourceMappingURL=|' static/fonts.min.css

# Remove build artifacts and cache
clean:
    rm -f {{binary}}
    rm -rf cache/
    echo "Cleaned up development artifacts"

# --- Docker targets ---

# Build the Docker image
docker-build:
    docker build -t {{docker_image}}:latest .

# Build and run the Docker container (detached)
docker-run: docker-build
    #!/usr/bin/env bash
    docker rm -f {{docker_container}} 2>/dev/null || true
    # Mount config.yaml read-only if it exists
    if test -f "$PWD/config.yaml"; then
        docker run -d \
            --name {{docker_container}} \
            --restart unless-stopped \
            -e PODIMO_BIND_HOST=0.0.0.0:12104 \
            -p 12104:12104 \
            -v "$PWD/cache:/tmp/podimo-rss-cache" \
            -v "$PWD/config.yaml:/etc/podimo-rss/config.yaml:ro" \
            {{docker_image}}:latest
    else
        docker run -d \
            --name {{docker_container}} \
            --restart unless-stopped \
            -e PODIMO_BIND_HOST=0.0.0.0:12104 \
            -p 12104:12104 \
            -v "$PWD/cache:/tmp/podimo-rss-cache" \
            {{docker_image}}:latest
    fi
    echo "Container '{{docker_container}}' started on http://localhost:12104"
    echo "View logs: docker logs -f {{docker_container}}"

# Stop and remove the running Docker container
docker-stop:
    docker stop {{docker_container}} 2>/dev/null || echo "Container '{{docker_container}}' not running"
    docker rm -f {{docker_container}} 2>/dev/null || true

# Follow Docker container logs
docker-logs:
    docker logs -f {{docker_container}} 2>/dev/null || echo "Container '{{docker_container}}' not running"

# --- Configuration ---

# Create config.yaml from config.example.yaml if it doesn't exist
init-config:
    test -f config.yaml || cp config.example.yaml config.yaml

# Create .env from .env.example if it doesn't exist
init-env:
    test -f .env || cp .env.example .env

# Edit configuration options interactively (YAML preferred)
config: init-config
    #!/usr/bin/env bash
    set -eu -o pipefail
    if ! command -v "${EDITOR:-nano}" >/dev/null 2>&1; then
        export EDITOR=nano
    fi
    if ! command -v "$EDITOR" >/dev/null 2>&1; then
        echo "Unable to find editor. Set EDITOR or install nano."
        exit 1
    fi
    read -e -p "You will open the config file config.yaml in the editor $EDITOR. Continue? [Y/n]> "
    [[ "$REPLY" != [nN]* ]] && "$EDITOR" config.yaml || exit 1
    echo 'Make sure to restart the service with "just restart" to apply the changes!'

# --- Legacy systemd targets ---

# Update to the latest Git release
update:
    #!/usr/bin/env bash
    set -eu -o pipefail
    CURRENT_GIT_TAG="$(git describe --abbrev=0 --tags)"
    echo "Current version is $CURRENT_GIT_TAG"
    echo "Fetching latest releases..."
    git fetch --tags
    UPDATE_GIT_TAG="$(git describe --tags "$(git rev-list --tags --max-count=1)")"
    if [[ "$CURRENT_GIT_TAG" == "$UPDATE_GIT_TAG" ]]; then
        echo "Already on the latest release $CURRENT_GIT_TAG!"
    else
        echo "Checking out latest release $UPDATE_GIT_TAG"
        git checkout "$UPDATE_GIT_TAG"
    fi
    echo "Updated to version $UPDATE_GIT_TAG"
    if test -f "config.yaml" && test -r "config.yaml"; then
        if ! git diff --name-only --no-index -- config.example.yaml config.yaml >/dev/null 2>&1; then
            echo
            echo "#############################################################"
            echo "# Your config differs from example config in                #"
            echo "# config.example.yaml! New options might not yet be set.    #"
            echo "#############################################################"
            echo
            git diff --no-index -- config.yaml config.example.yaml || true
        fi
    fi
    if test -f ".env"; then
        if test -r ".env"; then
            git diff --name-only --no-index -- .env.example .env >/dev/null || {
                echo
                echo "#############################################################"
                echo "# Your config differs from example config in .env.example!  #"
                echo "# New configuration options might not yet be in .env.       #"
                echo "#############################################################"
                echo
            }
            git diff --no-index -- .env .env.example || true
        else
            echo ".env file exists, but cannot be read"
            exit 1
        fi
    fi

# Enable and start the systemd service
start:
    sudo systemctl enable --now podimo.service

# Restart the systemd service
restart:
    sudo systemctl restart podimo.service

# Disable and stop the systemd service
stop:
    sudo systemctl disable --now podimo.service

# View systemd service status
status:
    sudo systemctl status podimo.service

# Follow systemd service logs
logs:
    sudo journalctl -f --since today -u podimo.service

# Build and install the tool as a systemd service
install: build
    #!/usr/bin/env bash
    set -eu -o pipefail
    cat > .podimo.service <<EOF
    # This is managed by $(pwd)/justfile
    [Unit]
    Description=Podimo to RSS converter
    After=network.target

    [Service]
    Type=simple
    User=$(id -un)
    Group=$(id -gn)
    WorkingDirectory=$(pwd)
    ExecStart=$(pwd)/{{binary}}
    Restart=always
    LimitNOFILE=infinity

    [Install]
    WantedBy=multi-user.target
    EOF
    chmod 644 .podimo.service
    sudo cp .podimo.service /etc/systemd/system/podimo.service
    rm -f .podimo.service
    sudo systemctl daemon-reload
    sudo systemctl enable podimo.service
    echo "Installed service! It will run as user $(id -un) and group $(id -gn)"

# Stop and remove the systemd service
uninstall: stop
    sudo rm -f /etc/systemd/system/podimo.service
    sudo systemctl daemon-reload
