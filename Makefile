BINARY   := golb
CMD_PATH := ./cmd/golb

.PHONY: build test vet fmt lint clean install-lint

build:
	go build -o $(BINARY) $(CMD_PATH)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: install-lint
	golangci-lint run ./...

clean:
	rm -f $(BINARY)

install-lint:
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
