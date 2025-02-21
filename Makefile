default: testacc

fmt:
	go fmt ./...
	terraform fmt -recursive

lint:
	golangci-lint run ./...

gen:
	go generate ./...

build: terraform-provider-coderd

terraform-provider-coderd: internal/provider/*.go main.go
	CGO_ENABLED=0 go build .

test: testacc
.PHONY: test

# Run acceptance tests
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m
.PHONY: testacc
