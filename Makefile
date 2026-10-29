.PHONY: build test race vet fmt clean

BINARY ?= bare-eye
BINDIR ?= bin

build:
	go build -o $(BINARY) ./cmd/bare-eye
	@mkdir -p $(BINDIR)
	@ln -sf ../$(BINARY) $(BINDIR)/bare-eye

test:
	go test ./...

race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

clean:
	rm -f $(BINARY) $(BINDIR)/bare-eye screenshot.png
