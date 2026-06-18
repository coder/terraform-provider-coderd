package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// useStateForUnknownUnlessChanged returns a string plan modifier for a Computed
// value that is derived server-side from another attribute (triggerAttr). Like
// the builtin stringplanmodifier.UseStateForUnknown it copies the prior state
// value into an unknown plan, but only while triggerAttr is unchanged. When
// triggerAttr changes the derived value may change too, so the planned value is
// left unknown for the server to recompute. Plain UseStateForUnknown would pin
// the stale value and cause "Provider produced inconsistent result after apply"
// once the source attribute changes.
//
// triggerAttr is a root-level attribute name (passed to path.Root).
func useStateForUnknownUnlessChanged(triggerAttr string) planmodifier.String {
	return useStateForUnknownUnlessChangedModifier{triggerAttr: triggerAttr}
}

type useStateForUnknownUnlessChangedModifier struct {
	triggerAttr string
}

func (m useStateForUnknownUnlessChangedModifier) Description(_ context.Context) string {
	return fmt.Sprintf("Preserves the prior value unless %q changes.", m.triggerAttr)
}

func (m useStateForUnknownUnlessChangedModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m useStateForUnknownUnlessChangedModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Do nothing if there is no state (resource is being created).
	if req.State.Raw.IsNull() {
		return
	}
	// Do nothing if there is a known planned value.
	if !req.PlanValue.IsUnknown() {
		return
	}
	// Do nothing if there is an unknown configuration value, otherwise
	// interpolation gets messed up.
	if req.ConfigValue.IsUnknown() {
		return
	}

	triggerPath := path.Root(m.triggerAttr)
	var planTrigger, stateTrigger attr.Value
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, triggerPath, &planTrigger)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, triggerPath, &stateTrigger)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A changed (or not-yet-known) trigger can change the derived value, so
	// leave the planned value unknown for the server to fill in.
	if !planTrigger.Equal(stateTrigger) {
		return
	}

	resp.PlanValue = req.StateValue
}
