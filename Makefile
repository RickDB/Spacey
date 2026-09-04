.PHONY: test run up down

test:
	go test ./...

run:
	go run ./cmd/spacey

up:
	docker compose up --build

down:
	docker compose down
