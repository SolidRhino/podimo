SHELL := bash
.ONESHELL:
.SHELLFLAGS := -eu -o pipefail -c
.DELETE_ON_ERROR:
MAKEFLAGS += --warn-undefined-variables
MAKEFLAGS += --no-builtin-rules

VENV=venv/bin/activate
PYTHON := python3
REQUIRED_MAJOR := 3
REQUIRED_MINOR := 10

help:
	@echo "The following options are available:"
	@echo ""
	@echo "$$ make test"
	@echo "\tRun the pytest test suite (requires venv)"
	@echo "$$ make lint"
	@echo "\tRun mypy type checker (requires venv)"
	@echo "$$ make clean"
	@echo "\tRemove cache, build artifacts, and venv"
	@echo ""
	@echo "$$ make docker-build"
	@echo "\tBuild the Docker image"
	@echo "$$ make docker-run"
	@echo "\tRun the Docker container (detached)"
	@echo "$$ make docker-stop"
	@echo "\tStop the running Docker container"
	@echo ""
	@echo "$$ make config"
	@echo "\tEdit configuration options for the tool"
	@echo "$$ make update"
	@echo "\tUpdate the tool to the latest release"
	@echo ""
	@echo "$$ make install"
	@echo "\tInstall the tool as a systemd service (venv + systemd)"
	@echo "$$ make start"
	@echo "$$ make restart"
	@echo "$$ make stop"
	@echo "\tChange the state of the systemd service"
	@echo "$$ make status"
	@echo "$$ make logs"
	@echo "\tView status and/or logs of the systemd service"
	@echo ""
	@echo "$$ make help"
	@echo "\tDisplay this help menu"
.PHONY: help

# --- Development targets ---

VENV:
	@# Check Python version
	@python_version="$$($(PYTHON) --version 2>&1 | awk '{print \$$2}')"
	@major="$$(echo $$python_version | cut -d. -f1)"
	@minor="$$(echo $$python_version | cut -d. -f2)"
	@if [[ "$$major" -lt "$(REQUIRED_MAJOR)" ]] || [[ "$$major" -eq "$(REQUIRED_MAJOR)" && "$$minor" -lt "$(REQUIRED_MINOR)" ]]; then
		@echo "Error: Python $(REQUIRED_MAJOR).$(REQUIRED_MINOR)+ required, but found $$python_version"
		@echo "Install Python $(REQUIRED_MINOR)+ and set PYTHON=path/to/python3 before running make."
		@exit 1
	@fi
	$(PYTHON) -m venv venv --prompt podimo

install-venv: VENV
	source venv/bin/activate
	pip install --upgrade pip
	pip install -r requirements.txt
.PHONY: install-venv

test: VENV
	@source venv/bin/activate
	python -m pytest tests/ -v
.PHONY: test

lint: VENV
	@source venv/bin/activate
	python -m mypy podimo/ main.py
.PHONY: lint

format:
	@echo "Format checking with ruff/black not configured. Add if needed."
	@source venv/bin/activate && python -m black --check podimo/ main.py tests/ 2>/dev/null || echo "Install black to enable: pip install black"
.PHONY: format

clean:
	rm -rf venv/ __pycache__/ .pytest_cache/ .mypy_cache/ .coverage htmlcov/ *.egg-info dist/ build/
	find . -type d -name __pycache__ -exec rm -rf {} + 2>/dev/null || true
	find . -type f -name '*.pyc' -delete 2>/dev/null || true
	find . -type d -name '*.egg-info' -exec rm -rf {} + 2>/dev/null || true
	@echo "Cleaned up development artifacts"
.PHONY: clean

# --- Docker targets ---

DOCKER_IMAGE := podimo-rss
DOCKER_CONTAINER := podimo-rss

docker-build:
	docker build -t $(DOCKER_IMAGE):latest .
.PHONY: docker-build

docker-run: docker-build
	@# Check if container already exists and remove it
	docker rm -f $(DOCKER_CONTAINER) 2>/dev/null || true
	docker run -d \
		--name $(DOCKER_CONTAINER) \
		--restart unless-stopped \
		-e PODIMO_BIND_HOST=0.0.0.0:12104 \
		-p 12104:12104 \
		-v $(PWD)/cache:/src/cache \
		$(DOCKER_IMAGE):latest
	@echo "Container '$(DOCKER_CONTAINER)' started on http://localhost:12104"
	@echo "View logs: docker logs -f $(DOCKER_CONTAINER)"
.PHONY: docker-run

docker-stop:
	@docker stop $(DOCKER_CONTAINER) 2>/dev/null || echo "Container '$(DOCKER_CONTAINER)' not running"
	@docker rm -f $(DOCKER_CONTAINER) 2>/dev/null || true
.PHONY: docker-stop

docker-logs:
	@docker logs -f $(DOCKER_CONTAINER) 2>/dev/null || echo "Container '$(DOCKER_CONTAINER)' not running"
.PHONY: docker-logs

# --- Legacy systemd targets ---

update: VENV
	@export CURRENT_GIT_TAG="$$(git describe --abbrev=0 --tags)"
	echo "Current version is $$CURRENT_GIT_TAG"
	echo "Fetching latest releases..."
	git fetch --tags
	export UPDATE_GIT_TAG="$$(git describe --tags $$(git rev-list --tags --max-count=1))"
	if [[ "$$CURRENT_GIT_TAG" == "$$UPDATE_GIT_TAG" ]]; then
		echo "Already on the latest release $$CURRENT_GIT_TAG!"
	else
		echo "Checkout out to latest release $$UPDATE_GIT_TAG"
		git checkout "$$UPDATE_GIT_TAG"
	fi
	echo "Updating dependencies..."
	source venv/bin/activate
	pip install -r requirements.txt >/dev/null
	echo "Updated to version $$UPDATE_GIT_TAG"
	if test -f ".env"; then
		if test -r ".env"; then
			git diff --name-only --no-index -- .env.example .env >/dev/null ||
			(echo -e "\n#############################################################"
			 echo -e   "# Your config differs from example config in .env.example!  #"
			 echo -e   "# This is not an issue, but new configuration options might #"
			 echo -e 	 "# not yet be present in your .env file.                     #"
			 echo -e 	 "#                                                           #"
			 echo -e 	 "#            The differences are shown below                #"
			 echo -e   "#############################################################\n")
			(git diff --no-index -- .env .env.example || true)
		else
			echo ".env file exists, but cannot be read"
			exit 1
		fi
	fi
.PHONY: update

config: .env
	@# Use the editor specified in the EDITOR environment variable,
	@# or default to nano otherwise.
	@which $$EDITOR &>/dev/null || export EDITOR=nano
	which $$EDITOR &>/dev/null || echo "Unable to find the nano binary. Either set the EDITOR environment variable to an editor of your choice or install nano"
	read -e -p "You will open the config file .env in the editor $$EDITOR. Continue? [Y/n]> "
	[[ "$$REPLY" != [nN]* ]] && $$EDITOR .env || exit 1
	echo "Make sure to restart the service with \"make restart\" to apply the changes!"

.env:
	cp .env.example .env

start:
	sudo systemctl enable --now podimo.service
.PHONY: start

restart:
	sudo systemctl restart podimo.service
.PHONY: restart

stop:
	sudo systemctl disable --now podimo.service
.PHONY: stop

status:
	sudo systemctl status podimo.service
.PHONY: status

logs:
	sudo journalctl -f --since today -u podimo.service
.PHONY: logs

install: VENV
	@cat > .podimo.service <<EOL
	# This is managed by $$(pwd)/Makefile
	[Unit]
	Description=Podimo to RSS converter
	After=network.target
	 
	[Service]
	Type=simple
	User=$$(id -un)
	Group=$$(id -gn)
	WorkingDirectory=$$(pwd)
	ExecStart=$$(pwd)/venv/bin/python main.py
	Restart=always
	LimitNOFILE=infinity
	 
	[Install]
	WantedBy=multi-user.target
	EOL
	chmod 644 .podimo.service
	sudo cp .podimo.service /etc/systemd/system/podimo.service
	rm -rf .podimo.service
	sudo systemctl daemon-reload
	sudo systemctl enable podimo.service
	echo "Installed service! It will run as user $$(id -un) and group $$(id -gn)"
.PHONY: install

uninstall: stop
	sudo rm -rf /etc/systemd/system/podimo.service
	sudo systemctl daemon-reload
.PHONY: uninstall
