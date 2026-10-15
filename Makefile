.PHONY: check build run

check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo "gofmt needed"; exit 1; }
	go vet ./...
	go test -race ./...
	node --check web/assets/app.js
	go build -o bin/v2v-web ./cmd/v2v-web

build:
	go build -o bin/v2v-web ./cmd/v2v-web

run: build
	./bin/v2v-web
