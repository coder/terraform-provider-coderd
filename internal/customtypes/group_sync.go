package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type groupSyncSettingsType struct {
	basetypes.MapType
}

var _ basetypes.MapTypable = GroupSyncSettingsType

var GroupSyncSettingsType = groupSyncSettingsType{}

func (t groupSyncSettingsType) ValueType(ctx context.Context) attr.Value {
	return GroupSyncSettings{}
}

// Equal implements basetypes.StringTypable.
func (t groupSyncSettingsType) Equal(o attr.Type) bool {
	if o, ok := o.(groupSyncSettingsType); ok {
		return t.MapType.Equal(o.MapType)
	}
	return false
}

// ValueFromString implements basetypes.StringTypable.
func (t groupSyncSettingsType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
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
		// takes a valid GroupSyncSettings instead of a string.
		return NewUUIDUnknown(), diags
	}

	return UUIDValue(value), diags
}

// ValueFromTerraform implements basetypes.StringTypable.
func (t groupSyncSettingsType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
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

type GroupSyncSettings struct {
	// The framework requires custom types extend a primitive or object.
	basetypes.MapValue
	value codersdk.GroupSyncSettings
}

var (
	_ basetypes.MapValuable       = GroupSyncSettings{}
	_ xattr.ValidateableAttribute = GroupSyncSettings{}
)

func NewGroupSyncSettingsNull() GroupSyncSettings {
	return GroupSyncSettings{
		MapValue: basetypes.NewMapNull(),
	}
}

func NewGroupSyncSettingsUnknown() GroupSyncSettings {
	return GroupSyncSettings{
		MapValue: basetypes.NewMapUnknown(),
	}
}

func GroupSyncSettingsValue(value uuid.UUID) UUID {
	return UUID{
		MapValue: basetypes.NewStringValue(value.String()),
		value:    value,
	}
}

// Equal implements basetypes.StringValuable.
func (v GroupSyncSettings) Equal(o attr.Value) bool {
	if o, ok := o.(GroupSyncSettings); ok {
		return v.StringValue.Equal(o.StringValue)
	}
	return false
}

// Type implements basetypes.StringValuable.
func (v GroupSyncSettings) Type(context.Context) attr.Type {
	return GroupSyncSettingsType
}

// ValueUUID returns the GroupSyncSettings value. If the value is null or unknown, returns the Nil GroupSyncSettings.
func (v GroupSyncSettings) ValueUUID() uuid.GroupSyncSettings {
	return v.value
}

// ValidateAttribute implements xattr.ValidateableAttribute.
func (v GroupSyncSettings) ValidateAttribute(ctx context.Context, req xattr.ValidateAttributeRequest, resp *xattr.ValidateAttributeResponse) {
	if v.IsNull() || v.IsUnknown() {
		return
	}

	if _, err := uuid.Parse(v.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid GroupSyncSettings",
			"The provided value cannot be parsed as a GroupSyncSettings\n\n"+
				"Path: "+req.Path.String()+"\n"+
				"Error: "+err.Error(),
		)
	}
}
