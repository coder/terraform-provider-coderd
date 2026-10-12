package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// aiUserBudgetOverrideMinVersion is the first Coder release that this resource
// supports.
const aiUserBudgetOverrideMinVersion = "2.36.0"

var (
	_ resource.Resource                = &AIUserBudgetOverrideResource{}
	_ resource.ResourceWithConfigure   = &AIUserBudgetOverrideResource{}
	_ resource.ResourceWithImportState = &AIUserBudgetOverrideResource{}
	_ resource.ResourceWithModifyPlan  = &AIUserBudgetOverrideResource{}
)

func NewAIUserBudgetOverrideResource() resource.Resource {
	return &AIUserBudgetOverrideResource{}
}

type AIUserBudgetOverrideResource struct {
	data *CoderdProviderData
}

type AIUserBudgetOverrideResourceModel struct {
	UserID     UUID        `tfsdk:"user_id"`
	GroupID    UUID        `tfsdk:"group_id"`
	SpendLimit types.Int64 `tfsdk:"spend_limit"`
}

// aiUserBudgetOverrideDiag converts an error from the user AI budget override
// API into a diagnostic. The upsert call returns 404 only when the group does
// not exist, or when the deployment has no user AI budget override API.
func aiUserBudgetOverrideDiag(action string, userID uuid.UUID, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	if isHTTPNotFound(err) {
		diags.AddError("Group Not Found or Unsupported Coder Version", fmt.Sprintf(
			"Unable to %s AI budget override for user %s: the deployment returned 404 for /api/v2/users/%s/ai/budget/override. "+
				"Either the group does not exist, or the deployment is older than Coder version %s, which this resource requires. "+
				"Original error: %s",
			action, userID, userID, aiUserBudgetOverrideMinVersion, err))
		return diags
	}
	diags.AddError("Client Error", fmt.Sprintf("Unable to %s AI budget override for user %s, got error: %s", action, userID, err))
	return diags
}

func aiUserBudgetOverrideConflictDiag(userID uuid.UUID) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.AddError("AI User Budget Override Already Exists", fmt.Sprintf(
		"Coder already has an AI budget override for user %s with a different group or spend limit. Terraform does not overwrite an override that it does not manage. "+
			"To manage the existing override, import it with the ID `%s`, then set the values that you want.",
		userID, userID))
	return diags
}

// sameAIUserBudgetOverride reports whether the plan matches an existing
// override.
func sameAIUserBudgetOverride(plan AIUserBudgetOverrideResourceModel, o codersdk.UserAIBudgetOverride) bool {
	return plan.GroupID.ValueUUID() == o.GroupID && plan.SpendLimit.ValueInt64() == o.SpendLimitMicros
}

// userOverride returns nil, nil when the user has no AI budget override.
func (r *AIUserBudgetOverrideResource) userOverride(ctx context.Context, userID uuid.UUID) (*codersdk.UserAIBudgetOverride, error) {
	override, err := r.data.Client.UserAIBudgetOverride(ctx, userID)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &override, nil
}

func (r *AIUserBudgetOverrideResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ai_user_budget_override"
}

// ModifyPlan fails the plan when a create would overwrite an override that
// Terraform does not manage. A create adopts an equal override, so a retry
// after a partially failed apply succeeds.
func (r *AIUserBudgetOverrideResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.data == nil {
		return
	}
	var plan AIUserBudgetOverrideResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.UserID.IsUnknown() || plan.GroupID.IsUnknown() || plan.SpendLimit.IsUnknown() {
		return
	}
	// Only a create or a replace writes an override that Terraform does not manage yet.
	if !req.State.Raw.IsNull() {
		var state AIUserBudgetOverrideResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || state.UserID.Equal(plan.UserID) {
			return
		}
	}
	existing, err := r.userOverride(ctx, plan.UserID.ValueUUID())
	if err != nil {
		// Best-effort. Create() repeats the check and reports the error.
		tflog.Debug(ctx, "skipping AI user budget override plan-time conflict check", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if existing != nil && !sameAIUserBudgetOverride(plan, *existing) {
		resp.Diagnostics.Append(aiUserBudgetOverrideConflictDiag(plan.UserID.ValueUUID())...)
	}
}

func (r *AIUserBudgetOverrideResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> **Warning**\nThis resource is only compatible with Coder version " + aiUserBudgetOverrideMinVersion + " and later.\n\n" +
			"Manages an AI spend limit for a single user, which [takes precedence](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#effective-group-resolution) over the user's `coderd_ai_group_budget` limits. " +
			"Requires a Premium license with the AI Gateway feature, and the Owner or User Admin role.\n\n" +
			"The override attributes the user's spend to a group, which the user must be a member of. " +
			"Coder removes the override when the user leaves that group or its organization, or when the user or the group is deleted.\n\n" +
			"-> If Coder already has a different override for the user, the plan fails. Import that override to manage it with Terraform.\n\n" +
			"Destroying this resource removes the override. Future requests from the user use their " +
			"[effective group](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#effective-group-resolution), " +
			"and spend already recorded stays attributed to the override's group.",
		Attributes: map[string]schema.Attribute{
			"user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user the override applies to. Changing this forces a new resource.",
				CustomType:          UUIDType,
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"group_id": schema.StringAttribute{
				MarkdownDescription: "ID of the group that the user's spend is attributed to. The user must be a member of the group.",
				CustomType:          UUIDType,
				Required:            true,
			},
			"spend_limit": schema.Int64Attribute{
				MarkdownDescription: "Spend limit for the user in each budget period, in integer micro-units: `1000000` is $1.00. " +
					"A limit of `0` allows no spend. The maximum is `1000000000000` ($1,000,000.00).",
				Required:   true,
				Validators: aiSpendLimitValidators,
			},
		},
	}
}

func (r *AIUserBudgetOverrideResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AIUserBudgetOverrideResource) upsert(ctx context.Context, plan AIUserBudgetOverrideResourceModel) error {
	_, err := r.data.Client.UpsertUserAIBudgetOverride(ctx, plan.UserID.ValueUUID(), codersdk.UpsertUserAIBudgetOverrideRequest{
		GroupID:          plan.GroupID.ValueUUID(),
		SpendLimitMicros: plan.SpendLimit.ValueInt64(),
	})
	return err
}

func (r *AIUserBudgetOverrideResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AIUserBudgetOverrideResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := plan.UserID.ValueUUID()
	existing, err := r.userOverride(ctx, userID)
	if err != nil {
		resp.Diagnostics.Append(aiUserBudgetOverrideDiag("read", userID, err)...)
		return
	}
	if existing != nil && !sameAIUserBudgetOverride(plan, *existing) {
		resp.Diagnostics.Append(aiUserBudgetOverrideConflictDiag(userID)...)
		return
	}
	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiUserBudgetOverrideDiag("set", userID, err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIUserBudgetOverrideResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AIUserBudgetOverrideResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := state.UserID.ValueUUID()
	override, err := r.userOverride(ctx, userID)
	if err != nil {
		resp.Diagnostics.Append(aiUserBudgetOverrideDiag("read", userID, err)...)
		return
	}
	if override == nil {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("AI budget override for user %s not found. Marking as deleted.", userID))
		resp.State.RemoveResource(ctx)
		return
	}

	state.GroupID = UUIDValue(override.GroupID)
	state.SpendLimit = types.Int64Value(override.SpendLimitMicros)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AIUserBudgetOverrideResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AIUserBudgetOverrideResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiUserBudgetOverrideDiag("update", plan.UserID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIUserBudgetOverrideResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AIUserBudgetOverrideResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := state.UserID.ValueUUID()
	err := r.data.Client.DeleteUserAIBudgetOverride(ctx, userID)
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.Append(aiUserBudgetOverrideDiag("delete", userID, err)...)
	}
}

// ImportState accepts a user ID or a username, like coderd_user.
func (r *AIUserBudgetOverrideResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	user, err := r.data.Client.User(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get user %q, expected a user ID or a username: %s", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), user.ID.String())...)
}
