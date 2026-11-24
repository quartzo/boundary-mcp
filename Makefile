.PHONY: build test fmt vet clean litmus litmus-sdk

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

litmus: litmus-sdk
	go run ./examples/litmus/verify -root examples/litmus

litmus-sdk:
	flatc --go -o examples/litmus/sdk/go examples/litmus/governance/protocol.fbs
