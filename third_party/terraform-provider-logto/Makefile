.DEFAULT_GOAL := verify
export GOMODCACHE ?= $(abspath ../.local/go-mod)
export GOCACHE ?= $(abspath ../.local/go-cache)
export GOPATH ?= $(abspath ../.local/go-path)
export TMPDIR ?= $(abspath ../.local/tmp)
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck
.PHONY: test test-race vet lint security build verify generate testacc test-integration fmt

test:
	mkdir -p $(TMPDIR)
	TF_ACC= go test ./...
test-race:
	mkdir -p $(TMPDIR)
	TF_ACC= go test -race -coverpkg=./... -coverprofile=coverage.out ./...
vet:
	TF_ACC= go vet ./...
lint:
	$(STATICCHECK) ./...
security:
	$(GOVULNCHECK) ./...
build:
	mkdir -p .work/bin
	go build -o .work/bin/terraform-provider-logto .
verify: test-race vet build
generate:
	mkdir -p $(TMPDIR)
	go generate ./...
testacc:
	TF_ACC=1 go test -v ./...
fmt:
	gofmt -w .

test-integration:
	@test -n "$(LOGTO_TEST_CREDENTIALS)" || (echo 'Set LOGTO_TEST_CREDENTIALS to disposable loopback credentials'; exit 1)
	TF_ACC= go test -race -tags=integration -run '^TestReal' -timeout=5m -v ./client ./provider
