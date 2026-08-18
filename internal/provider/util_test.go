package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/stretchr/testify/require"
)

func TestBoolPtrOrNil(t *testing.T) {
	t.Parallel()

	t.Run("null", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, boolPtrOrNil(types.BoolNull()))
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		require.Nil(t, boolPtrOrNil(types.BoolUnknown()))
	})

	t.Run("false", func(t *testing.T) {
		t.Parallel()
		value := boolPtrOrNil(types.BoolValue(false))
		require.NotNil(t, value)
		require.False(t, *value)
	})

	t.Run("true", func(t *testing.T) {
		t.Parallel()
		value := boolPtrOrNil(types.BoolValue(true))
		require.NotNil(t, value)
		require.True(t, *value)
	})
}
