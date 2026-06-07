.PHONY: build run deploy clean

# Build the streaming server binary for linux/amd64.
# Uses CGO_ENABLED=0 for a fully static binary with no libc dependency.
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-ldflags="-s -w" \
		-o streaming-server \
		./server/

# Run the server locally using default configuration.
# Set environment variables to override defaults (see .env.example).
run:
	go run ./server/

# Deploy to a remote Ubuntu server via SSH.
# Requires SSH access to the target host.
# Usage: make deploy HOST=<ip-or-hostname>
deploy:
	@if [ -z "$(HOST)" ]; then \
		echo "Usage: make deploy HOST=<ip-or-hostname>"; \
		exit 1; \
	fi
	ssh ubuntu@$(HOST) 'bash -s' < scripts/setup.sh

# Remove build artifacts.
clean:
	rm -f streaming-server
	go clean -cache
