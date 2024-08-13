# terraform-provider-coderd

`terraform-provider-coderd` enables managing a [Coder](https://github.com/coder/coder) deployment using [Terraform](https://github.com/hashicorp/terraform) IaC.

The provider currently supports resources and data sources for:
- Users
- Templates + Template Versions
- Groups
- Workspace Proxies
- Organizations (Data Source only)

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.21
- [Coder](https://github.com/coder/coder) >= 2.10.1

## Usage

See the [`examples`](examples) and the [documentation](https://registry.terraform.io/providers/coder/coderd/latest/docs).

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `make gen`.

### Terraform Acceptance Tests

Acceptance tests are run against a live Coder deployment in a local Docker container. To run the full suite of Acceptance tests, run `make testacc`.

> [!NOTE]
> Our [CI workflow](./github/workflows/test.yml) runs an acceptance test matrix against multiple Terraform versions.
