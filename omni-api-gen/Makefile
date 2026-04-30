BINARY := omni-api-gen
OUTPUT ?= ../public/omni/reference/api.mdx

.PHONY: build
build: ## Build the binary
	go build -o $(BINARY) .

.PHONY: run
run: ## Generate api.mdx using local Go build
	go run . --output $(OUTPUT)

.PHONY: test
test: ## Run tests
	go test ./...

.PHONY: tidy
tidy: ## Tidy Go module dependencies
	go mod tidy

.PHONY: clean
clean: ## Remove built binary
	rm -f $(BINARY)

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-15s\033[0m %s\n", $$1, $$2}'
