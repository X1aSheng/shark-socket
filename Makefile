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

build-production:
	go build -ldflags="-s -w" -o bin/shark-socket ./cmd/shark-socket/

docker-build:
	docker build -f deploy/docker/Dockerfile -t shark-socket:latest .

docker-compose-up:
	docker compose -f deploy/docker/docker-compose.yml up -d

docker-compose-dev:
	docker compose -f deploy/docker/docker-compose.yml --profile dev up

helm-lint:
	helm lint deploy/k8s/helm/shark-socket/

k8s-validate:
	kubectl apply -k deploy/k8s/app/ --dry-run=client

all: vet build test
