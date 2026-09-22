GOBIN := $(shell go env GOPATH)/bin
export PATH := $(PATH):$(GOBIN)
export CGO_ENABLED=0

.PHONY: all tools fmt vet lint test test-integration generate check-generated build web ci vuln licenses sbom secrets

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
	cd web && npm run generate

check-generated: generate
	git diff --exit-code -- internal/httpapi/gen tests/e2e/client internal/*/internal/db web/src/api/schema.d.ts

# -race требует cgo; сборка бинарников остаётся без cgo (инвариант 9).
test:
	CGO_ENABLED=1 go test -race -count=1 -coverprofile=coverage.out ./...

test-integration:
	CGO_ENABLED=1 go test -race -count=1 -tags integration ./...

build:
	go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api
	go build -trimpath -ldflags="-s -w" -o bin/worker ./cmd/worker
	go build -trimpath -ldflags="-s -w" -o bin/migrate ./cmd/migrate
	go build -trimpath -ldflags="-s -w" -o bin/dev-token ./cmd/dev-token

web:
	cd web && npm run lint && npm run typecheck && npm test && npm run build

vuln:
	govulncheck ./...

licenses:
	go-licenses check --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,Zlib,MPL-2.0 ./...

sbom:
	cyclonedx-gomod app -json -licenses -output sbom.api.json -main cmd/api .

secrets:
	gitleaks detect --source . --no-banner

ci: fmt vet lint test build
