package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

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

// agentsModelConfigSortedJSON re-encodes a JSON document with object keys sorted
// alphabetically (recursively) and compact spacing, matching Terraform's
// jsonencode output. Numbers are preserved verbatim via json.Number, so the only
// change is key order. Coder stores model_config in the SDK struct's field order,
// which is not alphabetical; without this the byte string in state never matches
// the user's jsonencode config, and the framework's raw-byte plan guard
// (server_planresourcechange.go: PlannedState.Raw.Equal(PriorState.Raw)) then
// marks the computed updated_at attribute unknown on every plan after import.
func agentsModelConfigSortedJSON(raw []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(v)
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

// agentsModelConfigDroppedKeys returns the dotted paths in raw that Coder
// silently discards when canonicalizing through codersdk.ChatModelCallConfig. A
// key is dropped iff removing it leaves the canonical output unchanged, so the
// SDK is the sole oracle and nothing here restates its schema.
func agentsModelConfigDroppedKeys(raw string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	root, ok := doc.(map[string]any)
	if !ok {
		_, err := agentsModelConfigCanonicalJSON(raw)
		return nil, err
	}
	baseline, err := agentsModelConfigCanonicalDoc(doc)
	if err != nil {
		return nil, err
	}
	dropped := map[string]struct{}{}
	if err := agentsModelConfigProbeDropped(doc, root, "", baseline, dropped); err != nil {
		return nil, err
	}
	var out []string
	for p := range dropped {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

func agentsModelConfigCanonicalDoc(doc any) (string, error) {
	encoded, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return agentsModelConfigCanonicalJSON(string(encoded))
}

// agentsModelConfigProbeDropped marks the path of every key whose removal leaves
// the canonical output unchanged, then recurses into surviving containers.
func agentsModelConfigProbeDropped(doc any, node map[string]any, prefix, baseline string, dropped map[string]struct{}) error {
	// Snapshot keys so mutating node during iteration is safe.
	keys := make([]string, 0, len(node))
	for k := range node {
		keys = append(keys, k)
	}
	for _, k := range keys {
		val := node[k]
		if agentsModelConfigIsContentFree(val) {
			continue
		}
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		delete(node, k)
		probe, err := agentsModelConfigCanonicalDoc(doc)
		node[k] = val
		if err != nil {
			return err
		}
		if probe == baseline {
			dropped[path] = struct{}{}
			continue
		}
		switch t := val.(type) {
		case map[string]any:
			if err := agentsModelConfigProbeDropped(doc, t, path, baseline, dropped); err != nil {
				return err
			}
		case []any:
			for _, el := range t {
				if m, ok := el.(map[string]any); ok {
					if err := agentsModelConfigProbeDropped(doc, m, path, baseline, dropped); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// agentsModelConfigIsContentFree reports whether a decoded JSON value is null or
// an empty array/object. The SDK's omitempty fields drop these regardless of
// whether the key is recognized, so removing one never changes the canonical
// output and the probe can't judge it. Scalars (including 0, false, "") carry
// config and are never content-free.
func agentsModelConfigIsContentFree(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	default:
		return false
	}
}

// agentsModelConfigNoDroppedKeysValidator rejects a model_config whose keys are
// silently discarded when Coder canonicalizes it. Without this a schema change
// in codersdk.ChatModelCallConfig (a removed or renamed field) would drop the
// user's setting with no plan-time error, leaving Terraform to consider the
// resulting state semantically equal. Erroring at plan time forces users to
// migrate to the current schema instead of losing configuration.
type agentsModelConfigNoDroppedKeysValidator struct{}

var _ validator.String = agentsModelConfigNoDroppedKeysValidator{}

func (v agentsModelConfigNoDroppedKeysValidator) Description(_ context.Context) string {
	return "model_config must not contain settings that Coder does not recognize and would silently discard."
}

func (v agentsModelConfigNoDroppedKeysValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v agentsModelConfigNoDroppedKeysValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	// Invalid or non-object JSON is left for the custom type and the not-empty
	// validator to report.
	dropped, err := agentsModelConfigDroppedKeys(req.ConfigValue.ValueString())
	if err != nil {
		return
	}
	if len(dropped) == 0 {
		return
	}
	resp.Diagnostics.AddAttributeError(
		req.Path,
		"Unrecognized model_config settings",
		fmt.Sprintf(
			"These model_config settings would be silently discarded by Coder: %s. "+
				"Remove them or update them to the current schema. See https://pkg.go.dev/github.com/coder/coder/v2/codersdk#ChatModelCallConfig.",
			strings.Join(dropped, ", "),
		),
	)
}
