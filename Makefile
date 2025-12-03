.PHONY: all build clean run-collector run-parser run-storage

# Define the main command directories
CMDS := collector parser storage

# Name of your module (replace with your go.mod name)
MODULE_NAME := github.com/pershinghar/go-distributed-collection-parsing

# --- Build Targets ---

all: build

build:
	@echo "Building all components..."
	@for cmd in $(CMDS); do \
		go build -o bin/$$cmd $(MODULE_NAME)/cmd/$$cmd ; \
	done
	@echo "Build complete. Executables are in the bin/ directory."

clean:
	@echo "Cleaning up build artifacts..."
	rm -rf bin/
	go clean
	@echo "Cleanup complete."

# --- Run Targets ---
# Note: These assume you have compiled the binaries using 'make build' first.

run-collector: build
	./bin/collector

run-parser: build
	./bin/parser

run-storage: build
	./bin/storage

# --- Utility Targets ---

get-deps:
	@echo "Getting and verifying dependencies..."
	go mod tidy
	go mod verify