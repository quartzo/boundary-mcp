.PHONY: build test fmt vet clean

build:
	go build -o bin/boundary-mcp ./cmd/boundary-mcp

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin
