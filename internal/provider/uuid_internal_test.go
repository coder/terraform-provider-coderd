package provider

import (
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/require"
)

var ValidUUID = uuid.New()

func TestUUIDTypeValueFromTerraform(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    tftypes.Value
		expected attr.Value
	}{
		{
			name:     "null",
			input:    tftypes.NewValue(tftypes.String, nil),
			expected: NewUUIDNull(),
		},
		{
			name:     "unknown",
			input:    tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			expected: NewUUIDUnknown(),
		},
		{
			name:     "valid UUID",
			input:    tftypes.NewValue(tftypes.String, ValidUUID.String()),
			expected: UUIDValue(ValidUUID),
		},
		{
			name:  "invalid UUID",
			input: tftypes.NewValue(tftypes.String, "invalid"),
			expected: UUID{
				StringValue: basetypes.NewStringValue("invalid"),
				value:       uuid.Nil,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			actual, err := uuidType.ValueFromTerraform(UUIDType, ctx, test.input)
			require.NoError(t, err)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestUUIDToStringValue(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		uuid     UUID
		expected types.String
	}{
		"value": {
			uuid:     UUIDValue(ValidUUID),
			expected: types.StringValue(ValidUUID.String()),
		},
		"null": {
			uuid:     NewUUIDNull(),
			expected: types.StringNull(),
		},
		"unknown": {
			uuid:     NewUUIDUnknown(),
			expected: types.StringUnknown(),
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			s, _ := test.uuid.ToStringValue(ctx)
			require.Equal(t, test.expected, s)
		})
	}
}
