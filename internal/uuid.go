package internal

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type uuidType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = UUIDType

var UUIDType = uuidType{}

// String implements basetypes.StringTypable.
func (t uuidType) String() string {
	return "UUID"
}

func (t uuidType) ValueType(_ context.Context) attr.Value {
	return UUID{}
}

// Equal implements basetypes.StringTypable.
func (t uuidType) Equal(o attr.Type) bool {
	if o, ok := o.(uuidType); ok {
		return t.StringType.Equal(o.StringType)
	}
	return false
}

// ValueFromString implements basetypes.StringTypable.
func (t uuidType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	var diags diag.Diagnostics

	if in.IsNull() {
		return NewUUIDNull(), diags
	}
	if in.IsUnknown() {
		return NewUUIDUnknown(), diags
	}

	value, err := uuid.Parse(in.ValueString())
	if err != nil {
		// The framework doesn't want us to return validation errors here
		// for some reason. They get caught by `ValidateAttribute` instead,
		// and this function isn't called directly by our provider - UUIDValue
		// takes a valid UUID instead of a string.
		return NewUUIDUnknown(), diags
	}

	return UUIDValue(value), diags
}

// ValueFromTerraform implements basetypes.StringTypable.
func (t uuidType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)

	if err != nil {
		return nil, err
	}

	stringValue, ok := attrValue.(basetypes.StringValue)

	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected basetypes.StringValue", attrValue)
	}

	stringValuable, diags := t.ValueFromString(ctx, stringValue)

	if diags.HasError() {
		return nil, fmt.Errorf("unexpected error converting StringValue to StringValuable: %v", diags)
	}

	return stringValuable, nil
}

type UUID struct {
	// The framework requires custom types extend a primitive or object.
	basetypes.StringValue
	value uuid.UUID
}

var (
	_ basetypes.StringValuable    = UUID{}
	_ xattr.ValidateableAttribute = UUID{}
)

func NewUUIDNull() UUID {
	return UUID{
		StringValue: basetypes.NewStringNull(),
	}
}

func NewUUIDUnknown() UUID {
	return UUID{
		StringValue: basetypes.NewStringUnknown(),
	}
}

func UUIDValue(value uuid.UUID) UUID {
	return UUID{
		StringValue: basetypes.NewStringValue(value.String()),
		value:       value,
	}
}

// Equal implements basetypes.StringValuable.
func (v UUID) Equal(o attr.Value) bool {
	if o, ok := o.(UUID); ok {
		return v.StringValue.Equal(o.StringValue)
	}
	return false
}

// Type implements basetypes.StringValuable.
func (v UUID) Type(context.Context) attr.Type {
	return UUIDType
}

// ValueUUID returns the UUID value. If the value is null or unknown, returns the Nil UUID.
func (v UUID) ValueUUID() uuid.UUID {
	return v.value
}

// ValidateAttribute implements xattr.ValidateableAttribute.
func (v UUID) ValidateAttribute(ctx context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}

	if _, err := uuid.Parse(v.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid UUID",
			"The provided value cannot be parsed as a UUID\n\n"+
				"Path: "+req.Path.String()+"\n"+
				"Error: "+err.Error(),
		)
	}
}
