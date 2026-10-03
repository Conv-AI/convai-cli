GO ?= go
GOFMT ?= gofmt
OAPI_CODEGEN_VERSION ?= v2.8.0
GOVULNCHECK_VERSION ?= v1.6.0

.PHONY: build test vet vuln fmt-check generate generate-check verify contract-check e2e e2e-auth

build:
	$(GO) build -trimpath -o bin/convai ./cmd/convai

test:
	$(GO) test -race ./...

e2e:
	$(GO) test -count=1 -timeout=30m -v -run '^TestStaging' ./tests/e2e

e2e-auth:
	CONVAI_E2E_REQUIRED=1 $(GO) test -count=1 -timeout=30m -v -run '^TestAuthReleaseStaging$$' ./tests/e2e

vet:
	$(GO) vet ./...

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

fmt-check:
	@files="$$(find . -name '*.go' -not -path './.tools/*')"; \
		test -z "$$($(GOFMT) -l $$files)"

generate:
	$(GO) run ./tools/generate -config
	$(GO) run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@$(OAPI_CODEGEN_VERSION) \
		--config api/oapi-codegen.yaml .tools/character.codegen.json
	$(GO) run ./tools/generate

generate-check:
	$(MAKE) generate
	git diff --exit-code -- api/oapi-codegen.yaml internal/characterapi/client.gen.go internal/api/dispatch.gen.go

contract-check:
	$(GO) run ./tools/check-contract https://api2-stg.convai.com/openapi.json

verify: fmt-check vet test generate-check build
