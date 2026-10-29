.PHONY: build test race vet fmt clean

BINARY ?= bare-eye

build:
	go build -o $(BINARY) ./cmd/bare-eye

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -f $(BINARY) screenshot.png
