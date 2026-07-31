GO ?= go
GOFMT ?= gofmt
OAPI_CODEGEN_VERSION ?= v2.8.0
GOVULNCHECK_VERSION ?= v1.6.0

.PHONY: build test vet vuln fmt-check generate generate-check verify

build:
	$(GO) build -trimpath -o bin/convai ./cmd/convai

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

fmt-check:
	@files="$$(find . -name '*.go' -not -path './.tools/*')"; \
		test -z "$$($(GOFMT) -l $$files)"

generate:
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
		--config api/oapi-codegen.yaml api/character.openapi.json

generate-check:
	$(MAKE) generate
	git diff --exit-code -- internal/characterapi/client.gen.go

verify: fmt-check vet test generate-check build
