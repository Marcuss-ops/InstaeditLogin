.PHONY: dev stop seed test lint backend-test frontend-test

# Start the full local development stack (Postgres + backend + frontend)
dev:
	docker compose up --build

# Stop the development stack
stop:
	docker compose down

# Apply development seed data (requires a running database and .env.dev)
seed:
	go run cmd/seed/main.go

# Run all tests (Go + frontend)
test: backend-test frontend-test

# Run Go tests with race detection
backend-test:
	go test -race ./...

# Run frontend lint, tests and build
frontend-test:
	cd web && npm ci && npm run lint && npm run test && npm run build

# Run formatters and linters
lint:
	gofmt -w .
	go vet ./...
	cd web && npm run lint
