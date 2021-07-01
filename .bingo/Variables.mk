# Auto generated binary variables helper managed by https://github.com/bwplotka/bingo v0.4.0. DO NOT EDIT.
# All tools are designed to be build inside $GOBIN.
BINGO_DIR := $(dir $(lastword $(MAKEFILE_LIST)))
GOPATH ?= $(shell go env GOPATH)
GOBIN  ?= $(firstword $(subst :, ,${GOPATH}))/bin
GO     ?= $(shell which go)

# Below generated variables ensure that every time a tool under each variable is invoked, the correct version
# will be used; reinstalling only if needed.
# For example for bingo variable:
#
# In your main Makefile (for non array binaries):
#
#include .bingo/Variables.mk # Assuming -dir was set to .bingo .
#
#command: $(BINGO)
#	@echo "Running bingo"
#	@$(BINGO) <flags/args..>
#
BINGO := $(GOBIN)/bingo-v0.4.0
$(BINGO): $(BINGO_DIR)/bingo.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/bingo-v0.4.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=bingo.mod -o=$(GOBIN)/bingo-v0.4.0 "github.com/bwplotka/bingo"

BUF := $(GOBIN)/buf-v0.41.0
$(BUF): $(BINGO_DIR)/buf.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/buf-v0.41.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=buf.mod -o=$(GOBIN)/buf-v0.41.0 "github.com/bufbuild/buf/cmd/buf"

GOLANGCI_LINT := $(GOBIN)/golangci-lint-v1.39.0
$(GOLANGCI_LINT): $(BINGO_DIR)/golangci-lint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/golangci-lint-v1.39.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=golangci-lint.mod -o=$(GOBIN)/golangci-lint-v1.39.0 "github.com/golangci/golangci-lint/cmd/golangci-lint"

GOMPLATE := $(GOBIN)/gomplate-v3.9.0
$(GOMPLATE): $(BINGO_DIR)/gomplate.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/gomplate-v3.9.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=gomplate.mod -o=$(GOBIN)/gomplate-v3.9.0 "github.com/hairyhenderson/gomplate/v3/cmd/gomplate"

GOVVV := $(GOBIN)/govvv-v0.3.0
$(GOVVV): $(BINGO_DIR)/govvv.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/govvv-v0.3.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=govvv.mod -o=$(GOBIN)/govvv-v0.3.0 "github.com/ahmetb/govvv"

GOX := $(GOBIN)/gox-v1.0.1
$(GOX): $(BINGO_DIR)/gox.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/gox-v1.0.1"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=gox.mod -o=$(GOBIN)/gox-v1.0.1 "github.com/mitchellh/gox"

KUSTOMIZE := $(GOBIN)/kustomize-v4.1.2
$(KUSTOMIZE): $(BINGO_DIR)/kustomize.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/kustomize-v4.1.2"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=kustomize.mod -o=$(GOBIN)/kustomize-v4.1.2 "sigs.k8s.io/kustomize/kustomize/v4"

MOCKERY := $(GOBIN)/mockery-v2.7.4
$(MOCKERY): $(BINGO_DIR)/mockery.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/mockery-v2.7.4"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=mockery.mod -o=$(GOBIN)/mockery-v2.7.4 "github.com/vektra/mockery/v2"

PROTOC_GEN_BUF_BREAKING := $(GOBIN)/protoc-gen-buf-breaking-v0.41.0
$(PROTOC_GEN_BUF_BREAKING): $(BINGO_DIR)/protoc-gen-buf-breaking.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/protoc-gen-buf-breaking-v0.41.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=protoc-gen-buf-breaking.mod -o=$(GOBIN)/protoc-gen-buf-breaking-v0.41.0 "github.com/bufbuild/buf/cmd/protoc-gen-buf-breaking"

PROTOC_GEN_BUF_LINT := $(GOBIN)/protoc-gen-buf-lint-v0.41.0
$(PROTOC_GEN_BUF_LINT): $(BINGO_DIR)/protoc-gen-buf-lint.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/protoc-gen-buf-lint-v0.41.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=protoc-gen-buf-lint.mod -o=$(GOBIN)/protoc-gen-buf-lint-v0.41.0 "github.com/bufbuild/buf/cmd/protoc-gen-buf-lint"

PROTOC_GEN_GO_GRPC := $(GOBIN)/protoc-gen-go-grpc-v1.1.0
$(PROTOC_GEN_GO_GRPC): $(BINGO_DIR)/protoc-gen-go-grpc.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/protoc-gen-go-grpc-v1.1.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=protoc-gen-go-grpc.mod -o=$(GOBIN)/protoc-gen-go-grpc-v1.1.0 "google.golang.org/grpc/cmd/protoc-gen-go-grpc"

PROTOC_GEN_GO := $(GOBIN)/protoc-gen-go-v1.26.0
$(PROTOC_GEN_GO): $(BINGO_DIR)/protoc-gen-go.mod
	@# Install binary/ries using Go 1.14+ build command. This is using bwplotka/bingo-controlled, separate go module with pinned dependencies.
	@echo "(re)installing $(GOBIN)/protoc-gen-go-v1.26.0"
	@cd $(BINGO_DIR) && $(GO) build -mod=mod -modfile=protoc-gen-go.mod -o=$(GOBIN)/protoc-gen-go-v1.26.0 "google.golang.org/protobuf/cmd/protoc-gen-go"

