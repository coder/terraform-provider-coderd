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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// agentsModelConfigType is a jsontypes.Normalized whose semantic equality also
// canonicalizes the document through codersdk.ChatModelCallConfig. Coder rewrites
// model_config on its side when it stores and returns it: decimal costs such as
// "3.00" come back as "3", and the legacy top-level pricing keys are folded into
// the nested "cost" object. A plain JSON string would therefore show a perpetual
// diff. Comparing the decoded structs instead lets Terraform treat the value the
// user wrote and the value Coder stores as equal, without the schema having to
// enumerate any fields.
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

// StringSemanticEquals treats two model_config documents as equal when they
// decode to the same codersdk.ChatModelCallConfig. The framework only invokes
// this when both values are known and non-null, so the canonical encodings can be
// compared directly. If either document fails to decode we fall back to
// jsontypes' JSON-level comparison; ValidateAttribute surfaces invalid JSON.
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

// agentsModelConfigNotEmptyValidator rejects a model_config that carries no
// settings, for example jsonencode({}). Coder collapses an all-zero
// ChatModelCallConfig (including an explicit "{}") to null when it stores and
// returns the value: see isZeroChatModelCallConfig in coderd/exp_chats.go.
// Because model_config is Optional (not Computed) and the framework skips
// semantic equality when either side is null, a configured "{}" would disagree
// with the null Coder returns and trip Terraform's "Provider produced
// inconsistent result after apply" check at apply time. An empty config is also
// meaningless: it is identical to omitting the attribute. Rejecting it at plan
// time turns that confusing core error into an actionable message that tells the
// user to omit the attribute instead.
//
// The check round-trips the value through the SDK struct rather than enumerating
// fields, so it stays correct as Coder adds tuning fields: a config that carries
// any set field canonicalizes to something other than "{}" and passes, matching
// Coder, which only collapses configs whose fields are all unset.
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
