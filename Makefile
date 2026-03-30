lint:
	golangci-lint run

test:
	go test -v ./...
	cd tests/integration && go test -v ./...

all: lint test
