.PHONY: build test check image
build:
	CGO_ENABLED=0 go build -trimpath -o bin/vikunja-github ./cmd/vikunja-github

test:
	go test -race ./...

check:
	go vet ./...
	go test -race ./...

image:
	docker build -t vikunja-github:local .
