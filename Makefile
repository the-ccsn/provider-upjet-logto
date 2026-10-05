SHELL := $(shell command -v bash)
.DEFAULT_GOAL := build
export GOMODCACHE ?= $(abspath ../.local/go-mod)
export GOCACHE ?= $(abspath ../.local/go-cache)
export GOPATH ?= $(abspath ../.local/go-path)
export TMPDIR ?= $(abspath ../.local/tmp)
export GODEBUG ?= netdns=cgo
export PATH := $(abspath .work/tools):$(PATH)
TF ?= $(shell command -v tofu || command -v terraform)
TF_PROVIDER_DIR := $(abspath third_party/terraform-provider-logto)
WORK_DIR := $(abspath .work)

.PHONY: schema generate test test-race test-integration vet lint security build verify snapshot sync-provider image package
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck
CONTAINER ?= docker
IMAGE ?= provider-upjet-logto:dev
VERSION ?= dev
CROSSPLANE ?= crossplane
SYFT ?= syft

snapshot:
	python3 hack/provider_snapshot.py
sync-provider:
	python3 hack/provider_snapshot.py --sync
test-race:
	mkdir -p $(TMPDIR)
	cd $(TF_PROVIDER_DIR) && TF_ACC= go test -race -coverpkg=./... -coverprofile=coverage.out ./...
	TF_ACC= go test -race -coverprofile=coverage.out ./...
test-integration: build
	@test -n "$(KUBEBUILDER_ASSETS)" || (echo 'Set KUBEBUILDER_ASSETS to local etcd, kube-apiserver and kubectl binaries'; exit 1)
	@for bin in etcd kube-apiserver kubectl; do test -x "$(KUBEBUILDER_ASSETS)/$$bin" || exit 1; done
	USE_EXISTING_CLUSTER=false TF_ACC= go test -race -tags=integration -timeout=10m -v ./test/integration
vet:
	cd $(TF_PROVIDER_DIR) && TF_ACC= go vet ./...
	go vet ./...
lint:
	$(STATICCHECK) ./...
security:
	$(GOVULNCHECK) ./...
image: build
	$(CONTAINER) build -f cluster/images/provider-upjet-logto/Dockerfile -t $(IMAGE) .
package: build snapshot
	python3 hack/build_runtime.py --arch "$$(go env GOARCH)"
	$(CROSSPLANE) xpkg build --package-root=package --examples-root=examples --embed-runtime-image-tarball=.work/runtime.tar --package-file=.work/provider-upjet-logto.xpkg
	$(SYFT) scan docker-archive:.work/runtime.tar -o spdx-json=.work/runtime.spdx.json
	python3 hack/verify_package.py

schema:
	mkdir -p $(WORK_DIR)/bin $(WORK_DIR)/schema
	cd $(TF_PROVIDER_DIR) && go build -o $(WORK_DIR)/bin/terraform-provider-logto .
	printf '%s\n' 'terraform {' ' required_providers {' '  logto = { source = "Lenstra/logto" }' ' }' '}' > $(WORK_DIR)/schema/main.tf
	printf '%s\n' 'provider_installation {' ' dev_overrides { "Lenstra/logto" = "$(WORK_DIR)/bin" }' ' direct {}' '}' > $(WORK_DIR)/schema/terraform.rc
	TF_CLI_CONFIG_FILE=$(WORK_DIR)/schema/terraform.rc $(TF) -chdir=$(WORK_DIR)/schema providers schema -json > config/schema.json.tmp
	mv config/schema.json.tmp config/schema.json

generate: schema
	mkdir -p $(WORK_DIR)/tools
	printf '%s\n' '#!/usr/bin/env bash' 'exec go tool goimports "$$@"' > $(WORK_DIR)/tools/goimports
	chmod +x $(WORK_DIR)/tools/goimports
	go run github.com/crossplane/upjet/v2/cmd/scraper -n Lenstra/logto -r $(TF_PROVIDER_DIR)/docs/resources -o config/provider-metadata.yaml
	go generate -tags generate ./apis

test:
	mkdir -p $(TMPDIR)
	cd $(TF_PROVIDER_DIR) && TF_ACC= go test ./...
	go test ./...

build:
	mkdir -p .work/bin
	CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/the-ccsn/provider-upjet-logto/internal/version.Version=$(VERSION)" -o .work/bin/provider ./cmd/provider

verify: snapshot test-race vet build
