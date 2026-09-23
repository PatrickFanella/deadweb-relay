.PHONY: run test build docker-up seed-content refresh-authored-content zip

run:
	go run ./cmd/relay

test:
	go test ./...

build:
	mkdir -p bin
	go build -o bin/relay ./cmd/relay

docker-up:
	docker compose up --build

seed-content:
	go run ./cmd/seed

refresh-authored-content:
	go run ./cmd/seed -refresh

zip:
	git archive --format=zip --output=../deadweb-relay.zip HEAD
