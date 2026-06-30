package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/attr/xattr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// agentsModelConfigType canonicalizes model_config through codersdk.ChatModelCallConfig
// so the value the user writes and the value Coder stores back compare equal.
type agentsModelConfigType struct {
	jsontypes.NormalizedType
}

var _ basetypes.StringTypable = agentsModelConfigType{}

// String implements basetypes.StringTypable.
func (t agentsModelConfigType) String() string {
	return "agentsModelConfigType"
}

// ValueType implements basetypes.StringTypable.
func (t agentsModelConfigType) ValueType(ctx context.Context) attr.Value {
	return agentsModelConfigValue{}
}

// Equal implements basetypes.StringTypable.
func (t agentsModelConfigType) Equal(o attr.Type) bool {
	if o, ok := o.(agentsModelConfigType); ok {
		return t.NormalizedType.Equal(o.NormalizedType)
	}
	return false
}

// ValueFromString implements basetypes.StringTypable.
func (t agentsModelConfigType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return agentsModelConfigValue{Normalized: jsontypes.Normalized{StringValue: in}}, nil
}

// ValueFromTerraform implements basetypes.StringTypable.
func (t agentsModelConfigType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.NormalizedType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	normalized, ok := attrValue.(jsontypes.Normalized)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected jsontypes.Normalized", attrValue)
	}
	return agentsModelConfigValue{Normalized: normalized}, nil
}

type agentsModelConfigValue struct {
	jsontypes.Normalized
}

var (
	_ basetypes.StringValuableWithSemanticEquals = agentsModelConfigValue{}
	_ xattr.ValidateableAttribute                = agentsModelConfigValue{}
)

func newAgentsModelConfigNull() agentsModelConfigValue {
	return agentsModelConfigValue{Normalized: jsontypes.NewNormalizedNull()}
}

func newAgentsModelConfigValue(value string) agentsModelConfigValue {
	return agentsModelConfigValue{Normalized: jsontypes.NewNormalizedValue(value)}
}

// Type implements basetypes.StringValuable.
func (v agentsModelConfigValue) Type(context.Context) attr.Type {
	return agentsModelConfigType{}
}

// Equal implements basetypes.StringValuable.
func (v agentsModelConfigValue) Equal(o attr.Value) bool {
	if o, ok := o.(agentsModelConfigValue); ok {
		return v.Normalized.Equal(o.Normalized)
	}
	return false
}

// StringSemanticEquals treats two model_config docs as equal when they decode to
// the same struct; falls back to JSON comparison if either fails to decode.
func (v agentsModelConfigValue) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	newValue, ok := newValuable.(agentsModelConfigValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected value type %T but got %T. Please report this to the provider developers.", v, newValuable),
		)
		return false, diags
	}

	current, err := agentsModelConfigCanonicalJSON(v.ValueString())
	if err != nil {
		return v.Normalized.StringSemanticEquals(ctx, newValue.Normalized)
	}
	proposed, err := agentsModelConfigCanonicalJSON(newValue.ValueString())
	if err != nil {
		return v.Normalized.StringSemanticEquals(ctx, newValue.Normalized)
	}

	return current == proposed, diags
}

// agentsModelConfigCanonicalJSON round-trips a model_config document through the
// SDK type so equivalent encodings compare equal. This mirrors the encoding Coder
// applies when it stores and returns the value.
func agentsModelConfigCanonicalJSON(raw string) (string, error) {
	var config codersdk.ChatModelCallConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// agentsModelConfigUseStateIfSemanticallyEqual keeps the prior state value when
// the configured model_config canonicalizes to the same JSON. The plugin
// framework only runs StringSemanticEquals against state during refresh/apply,
// never against the (jsonencode-sorted) config during plan, so without this a
// key-order-only difference between state and config yields a perpetual no-op
// diff. This surfaces after `terraform import` (Read stores Coder's struct-order
// JSON, which the alphabetical jsonencode config never matches byte-for-byte).
type agentsModelConfigUseStateIfSemanticallyEqual struct{}

var _ planmodifier.String = agentsModelConfigUseStateIfSemanticallyEqual{}

func (agentsModelConfigUseStateIfSemanticallyEqual) Description(_ context.Context) string {
	return "Keeps the prior model_config when the configured value is semantically equal."
}

func (m agentsModelConfigUseStateIfSemanticallyEqual) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (agentsModelConfigUseStateIfSemanticallyEqual) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	stateCanon, err := agentsModelConfigCanonicalJSON(req.StateValue.ValueString())
	if err != nil {
		return
	}
	configCanon, err := agentsModelConfigCanonicalJSON(req.ConfigValue.ValueString())
	if err != nil {
		return
	}
	if stateCanon == configCanon {
		resp.PlanValue = req.StateValue
	}
}

// agentsModelConfigNotEmptyValidator rejects an empty model_config (e.g. jsonencode({})):
// Coder collapses it to null, which would trip Terraform's post-apply consistency check.
type agentsModelConfigNotEmptyValidator struct{}

var _ validator.String = agentsModelConfigNotEmptyValidator{}

func (v agentsModelConfigNotEmptyValidator) Description(_ context.Context) string {
	return "model_config must contain at least one setting; omit the attribute entirely to use Coder's defaults."
}

func (v agentsModelConfigNotEmptyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v agentsModelConfigNotEmptyValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	// Invalid JSON is left for the custom type's ValidateAttribute to report.
	canonical, err := agentsModelConfigCanonicalJSON(req.ConfigValue.ValueString())
	if err != nil {
		// Report valid JSON that can't decode into the SDK config (e.g. an array or primitive).
		if json.Valid([]byte(req.ConfigValue.ValueString())) {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid model_config",
				"model_config must be a JSON object compatible with Coder's chat model config schema.",
			)
		}
		return
	}
	if canonical == "{}" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Empty model_config",
			"model_config has no settings, so Coder would discard it and leave Terraform's state inconsistent. Omit the attribute entirely to use Coder's defaults.",
		)
	}
}
