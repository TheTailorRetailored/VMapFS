.PHONY: build test clean

build:
	go build -o bin/vmapfs ./cmd/vmapfs

test:
	go test -v ./...

clean:
	rm -rf bin/
	go clean

run: build
	./bin/vmapfs $(ARGS)