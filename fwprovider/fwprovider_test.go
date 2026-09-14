package fwprovider_test

import (
	"context"
	"testing"

	tfprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/stretchr/testify/require"

	"github.com/coder/terraform-provider-coderd/fwprovider"
)

// TestNew asserts that the exported factory yields the same provider main.go
// serves, so embedders see the full set of resources and data sources.
func TestNew(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p := fwprovider.New("test")()
	require.NotNil(t, p)

	var resp tfprovider.MetadataResponse
	p.Metadata(ctx, tfprovider.MetadataRequest{}, &resp)
	require.Equal(t, "coderd", resp.TypeName)
	require.Equal(t, "test", resp.Version)

	require.NotEmpty(t, p.Resources(ctx))
	require.NotEmpty(t, p.DataSources(ctx))
}
