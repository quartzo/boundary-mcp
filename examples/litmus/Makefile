.PHONY: verify sdk protocol-test

verify:
	go run ./verify -root .

protocol-test:
	go run ./protocoltest -governance governance

sdk:
	flatc --go -o sdk/go governance/protocol.fbs
