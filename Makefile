.PHONY: build test fmt vet clean litmus litmus-sdk protocol-test judge-eval judge-calibrate judge-e2e version-snapshot version-compat version-list

VERSION_GOVERNANCE ?= examples/litmus/governance
VERSION_ALIAS ?= v0
VERSION_BASE ?= v0

build:
	go build -o bin/boundary-mcp ./cmd/boundary-mcp
	go build -o bin/versionctl ./cmd/versionctl

test:
	go test ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin

litmus: litmus-sdk protocol-test
	go run ./examples/litmus/verify -root examples/litmus

protocol-test:
	go run ./examples/litmus/protocoltest -governance examples/litmus/governance

litmus-sdk:
	flatc --go -o examples/litmus/sdk/go examples/litmus/governance/protocol.fbs

judge-eval:
	go run ./cmd/judge-eval -cases internal/judge/testdata/cases

judge-calibrate:
	go run ./cmd/judge-eval -cases synthetic

judge-e2e:
	go test -tags=live ./internal/compiler -run TestLiveJudgeProposal -count=1 -v

version-snapshot:
	go run ./cmd/versionctl snapshot -governance $(VERSION_GOVERNANCE) -alias $(VERSION_ALIAS)

version-compat:
	go run ./cmd/versionctl compat -governance $(VERSION_GOVERNANCE) -base $(VERSION_BASE)

version-list:
	go run ./cmd/versionctl list -governance $(VERSION_GOVERNANCE)
