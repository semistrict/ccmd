.PHONY: install check

install:
	go install .

check:
	golangci-lint run ./...
