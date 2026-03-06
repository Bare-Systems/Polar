APP=polar

.PHONY: build run test lint vet race integration tidy cross

build:
	go build ./...

run:
	go run ./cmd/polar -config ./configs/simulator.json

test:
	go test ./...

race:
	go test -race ./...

lint:
	golangci-lint run

vet:
	go vet ./...

integration:
	go test ./test/... -v

tidy:
	go mod tidy

cross:
	GOOS=linux GOARCH=amd64 go build -o bin/$(APP)-linux-amd64 ./cmd/polar
	GOOS=linux GOARCH=arm64 go build -o bin/$(APP)-linux-arm64 ./cmd/polar
