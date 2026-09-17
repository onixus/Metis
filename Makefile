GOBIN := $(shell go env GOPATH)/bin
export PATH := $(PATH):$(GOBIN)
export CGO_ENABLED=0

.PHONY: all tools fmt vet lint test test-integration generate build ci vuln licenses sbom secrets

all: ci

tools:
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	go install github.com/google/go-licenses/v2@latest
	go install github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest

fmt:
	gofmt -l . | tee /dev/stderr | test -z "$$(cat)"

vet:
	go vet ./...

lint:
	golangci-lint run ./...

generate:
	oapi-codegen -config api/oapi-server.yaml api/openapi.yaml
	oapi-codegen -config api/oapi-types.yaml api/openapi.yaml
	sqlc generate

test:
	go test -race -count=1 -coverprofile=coverage.out ./...

test-integration:
	go test -race -count=1 -tags integration ./...

build:
	go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api
	go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker

vuln:
	govulncheck ./...

licenses:
	go-licenses check --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,Zlib,MPL-2.0 ./...

sbom:
	cyclonedx-gomod app -json -licenses -output sbom.api.json -main cmd/api .

secrets:
	gitleaks detect --source . --no-banner

ci: fmt vet lint test build
