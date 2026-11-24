.PHONY: verify sdk

verify:
	go run ./verify -root .

sdk:
	flatc --go -o sdk/go governance/protocol.fbs
