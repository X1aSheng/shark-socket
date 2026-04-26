.PHONY: build test vet lint benchmark race clean tidy examples docker

build:
	go build ./...

test:
	go test ./... -count=1

vet:
	go vet ./...

lint:
	golangci-lint run ./...

benchmark:
	go test -bench=. -benchmem -count=5 ./...

race:
	go test -race ./...

clean:
	rm -rf bin/ dist/

tidy:
	go mod tidy

examples:
	@for d in examples/*/; do \
		echo "Building $$d..." && (cd "$$d" && go build .) || exit 1; \
	done

docker:
	docker build -t shark-socket .

all: vet build test
