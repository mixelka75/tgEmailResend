.PHONY: build run clean docker docker-run test lint help

# Binary name
BINARY=emailbot

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GORUN=$(GOCMD) run
GOTEST=$(GOCMD) test
GOMOD=$(GOCMD) mod

# Build flags
LDFLAGS=-ldflags="-s -w"

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	CGO_ENABLED=1 $(GOBUILD) $(LDFLAGS) -o $(BINARY) ./cmd/bot

run: ## Run the bot
	$(GORUN) ./cmd/bot

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf data/*.db

deps: ## Download dependencies
	$(GOMOD) download
	$(GOMOD) tidy

docker: ## Build Docker image
	docker build -t emailbot .

docker-run: ## Run with Docker Compose
	docker compose up -d

docker-stop: ## Stop Docker Compose
	docker compose down

test: ## Run tests
	$(GOTEST) -v ./...

lint: ## Run linter
	golangci-lint run

generate-key: ## Generate encryption key
	@openssl rand -base64 24 | head -c 32 && echo
