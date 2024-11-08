default: testacc

fmt:
	go fmt ./...
	terraform fmt -recursive

vet:
	go vet ./...

gen:
	go generate ./...

build: terraform-provider-coderd

terraform-provider-coderd: internal/provider/*.go main.go
	CGO_ENABLED=0 go build .

# Run acceptance tests
.PHONY: testacc
testacc:
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m
