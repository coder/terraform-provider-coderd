package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// aiGroupBudgetMinVersion is the first Coder release that this resource
// supports.
const aiGroupBudgetMinVersion = "2.36.0"

var (
	_ resource.Resource                = &AIGroupBudgetResource{}
	_ resource.ResourceWithConfigure   = &AIGroupBudgetResource{}
	_ resource.ResourceWithImportState = &AIGroupBudgetResource{}
	_ resource.ResourceWithModifyPlan  = &AIGroupBudgetResource{}
)

func NewAIGroupBudgetResource() resource.Resource {
	return &AIGroupBudgetResource{}
}

type AIGroupBudgetResource struct {
	data *CoderdProviderData
}

type AIGroupBudgetResourceModel struct {
	GroupID    UUID        `tfsdk:"group_id"`
	SpendLimit types.Int64 `tfsdk:"spend_limit"`
}

// aiSpendLimitValidators bound a spend limit to the range that Coder accepts.
var aiSpendLimitValidators = []validator.Int64{
	int64validator.Between(0, codersdk.MaxAISpendLimitMicros),
}

// aiGroupBudgetDiag converts an error from the group AI budget API into a
// diagnostic. Coder returns 404 when the group does not exist, when the token
// can read the group but cannot update it, or when the deployment has no group
// AI budget API.
func aiGroupBudgetDiag(action string, groupID uuid.UUID, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	if isHTTPNotFound(err) {
		diags.AddError("Group Not Found, Insufficient Permissions, or Unsupported Coder Version", fmt.Sprintf(
			"Unable to %s AI budget for group %s: the deployment returned 404 for /api/v2/groups/%s/ai/budget. "+
				"This resource requires an existing group, Coder version %s or later, and a token with permission to update the group. "+
				"Original error: %s",
			action, groupID, groupID, aiGroupBudgetMinVersion, err))
		return diags
	}
	diags.AddError("Client Error", fmt.Sprintf("Unable to %s AI budget for group %s, got error: %s", action, groupID, err))
	return diags
}

func aiGroupBudgetConflictDiag(groupID uuid.UUID) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.AddError("AI Group Budget Already Exists", fmt.Sprintf(
		"Coder already has an AI budget for group %s with a different spend limit. Terraform does not overwrite a budget that it does not manage. "+
			"To manage the existing budget, import it with the ID `%s`, then set the spend limit that you want.",
		groupID, groupID))
	return diags
}

// groupBudget returns nil, nil when the group has no AI budget.
func (r *AIGroupBudgetResource) groupBudget(ctx context.Context, groupID uuid.UUID) (*codersdk.GroupAIBudget, error) {
	budget, err := r.data.Client.GroupAIBudget(ctx, groupID)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &budget, nil
}

func (r *AIGroupBudgetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ai_group_budget"
}

// ModifyPlan fails the plan when a create would overwrite a budget that
// Terraform does not manage. A create adopts an equal budget, so a retry after
// a partially failed apply succeeds.
func (r *AIGroupBudgetResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.data == nil {
		return
	}
	var plan AIGroupBudgetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.GroupID.IsUnknown() || plan.SpendLimit.IsUnknown() {
		return
	}
	// Only a create or a replace writes a budget that Terraform does not manage yet.
	if !req.State.Raw.IsNull() {
		var state AIGroupBudgetResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.GroupID.Equal(plan.GroupID) {
			return
		}
	}
	existing, err := r.groupBudget(ctx, plan.GroupID.ValueUUID())
	if err != nil {
		// Best-effort. Create() repeats the check and reports the error.
		tflog.Debug(ctx, "skipping AI group budget plan-time conflict check", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if existing != nil && existing.SpendLimitMicros != plan.SpendLimit.ValueInt64() {
		resp.Diagnostics.Append(aiGroupBudgetConflictDiag(plan.GroupID.ValueUUID())...)
	}
}

func (r *AIGroupBudgetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> **Warning**\nThis resource is only compatible with Coder version " + aiGroupBudgetMinVersion + " and later.\n\n" +
			"Manages the AI spend limit that AI Gateway applies to each member of a group. " +
			"Requires a Premium license with the AI Gateway feature, and the Owner or User Admin role, " +
			"or the Organization Admin or Organization User Admin role in the group's organization.\n\n" +
			"Each member of the group can spend up to the limit in each [budget period](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#deployment-settings). " +
			"When a user belongs to several groups with a budget, the deployment's [budget policy](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#effective-group-resolution) selects the group that applies.\n\n" +
			"-> If Coder already has a different budget for the group, the plan fails. Import that budget to manage it with Terraform.\n\n" +
			"Destroying this resource removes the budget. Future requests from members of the group use their next " +
			"[effective group](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#effective-group-resolution), " +
			"and spend already recorded stays attributed to this group. " +
			"Coder also removes the budget when the group is deleted.",
		Attributes: map[string]schema.Attribute{
			"group_id": schema.StringAttribute{
				MarkdownDescription: "ID of the group the budget applies to. Changing this forces a new resource.",
				CustomType:          UUIDType,
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"spend_limit": schema.Int64Attribute{
				MarkdownDescription: "Spend limit for each member of the group in each budget period, in integer micro-units: `1000000` is $1.00. " +
					"A limit of `0` blocks AI Gateway requests from members whose effective group is this group. The maximum is `1000000000000` ($1,000,000.00).",
				Required:   true,
				Validators: aiSpendLimitValidators,
			},
		},
	}
}

func (r *AIGroupBudgetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*CoderdProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.data = data
}

func (r *AIGroupBudgetResource) upsert(ctx context.Context, plan AIGroupBudgetResourceModel) error {
	_, err := r.data.Client.UpsertGroupAIBudget(ctx, plan.GroupID.ValueUUID(), codersdk.UpsertGroupAIBudgetRequest{
		SpendLimitMicros: plan.SpendLimit.ValueInt64(),
	})
	return err
}

func (r *AIGroupBudgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AIGroupBudgetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := plan.GroupID.ValueUUID()
	existing, err := r.groupBudget(ctx, groupID)
	if err != nil {
		resp.Diagnostics.Append(aiGroupBudgetDiag("read", groupID, err)...)
		return
	}
	if existing != nil && existing.SpendLimitMicros != plan.SpendLimit.ValueInt64() {
		resp.Diagnostics.Append(aiGroupBudgetConflictDiag(groupID)...)
		return
	}
	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiGroupBudgetDiag("set", groupID, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIGroupBudgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AIGroupBudgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := state.GroupID.ValueUUID()
	budget, err := r.groupBudget(ctx, groupID)
	if err != nil {
		resp.Diagnostics.Append(aiGroupBudgetDiag("read", groupID, err)...)
		return
	}
	if budget == nil {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("AI budget for group %s not found. Marking as deleted.", groupID))
		resp.State.RemoveResource(ctx)
		return
	}

	state.SpendLimit = types.Int64Value(budget.SpendLimitMicros)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AIGroupBudgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AIGroupBudgetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiGroupBudgetDiag("update", plan.GroupID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIGroupBudgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AIGroupBudgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := state.GroupID.ValueUUID()
	err := r.data.Client.DeleteGroupAIBudget(ctx, groupID)
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(aiGroupBudgetDiag("delete", groupID, err)...)
	}
}

// ImportState accepts a group ID, or <organization-name>/<group-name> like
// coderd_group.
func (r *AIGroupBudgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	orgName, groupName, ok := strings.Cut(req.ID, "/")
	if !ok {
		groupID, err := uuid.Parse(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Expected a group ID or <organization-name>/<group-name>, got %q: %s", req.ID, err))
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), groupID.String())...)
		return
	}

	org, err := r.data.Client.OrganizationByName(ctx, orgName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get organization with name %s: %s", orgName, err))
		return
	}
	group, err := r.data.Client.GroupByOrgAndName(ctx, org.ID, groupName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get group with name %s: %s", groupName, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), group.ID.String())...)
}
