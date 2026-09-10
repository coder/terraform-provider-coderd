// Package fwprovider exposes the coderd Terraform provider implementation to
// other Go programs that embed it instead of running it as a plugin binary.
//
// The implementation lives in internal/provider, which the Go toolchain keeps
// unimportable from outside this module. Embedders need a provider.Provider
// value so they can read the schema and drive CRUD in-process; this package
// hands them one without widening the rest of the internal API.
package fwprovider

import (
	tfprovider "github.com/hashicorp/terraform-plugin-framework/provider"

	"github.com/coder/terraform-provider-coderd/internal/provider"
)

// New returns a factory for the coderd provider implementation. The signature
// matches what providerserver.Serve expects, so embedders can either serve it
// as a plugin or call the factory directly to get an instance. version is what
// the provider reports from its Metadata method.
func New(version string) func() tfprovider.Provider {
	return provider.New(version)
}
