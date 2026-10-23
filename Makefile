GO ?= go
GOTOOLCHAIN ?= go1.27.1

.PHONY: dev test vet build build-windows-x64 format

dev:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) tool mygo dev

test:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) test ./...

vet:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) vet ./...
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) tool mygo vet .

build:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) tool mygo build

build-windows-x64:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) tool mygo build -platform windows/amd64

format:
	env GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO) fmt ./...
