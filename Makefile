BINARY := bin/klog

.PHONY: build test vet run clean install

build:
	go build -o $(BINARY) ./cmd/klog

test:
	go test ./...

vet:
	go vet ./...

run: build
	./$(BINARY) $(ARGS)

install:
	go install ./cmd/klog

clean:
	rm -rf bin
