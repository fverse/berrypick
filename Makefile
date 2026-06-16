BINARY := berrypick

.PHONY: build install test vet tidy clean

build:
	go build -o $(BINARY) .

install:
	go install .

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -f $(BINARY)
