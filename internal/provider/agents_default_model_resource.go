package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// agentsDefaultModelMinVersion is the first Coder release that can include the
// organization-scoped chat model API.
const agentsDefaultModelMinVersion = "2.37.0"

var (
	_ resource.Resource                = &AgentsDefaultModelResource{}
	_ resource.ResourceWithConfigure   = &AgentsDefaultModelResource{}
	_ resource.ResourceWithImportState = &AgentsDefaultModelResource{}
	_ resource.ResourceWithModifyPlan  = &AgentsDefaultModelResource{}
)

func NewAgentsDefaultModelResource() resource.Resource {
	return &AgentsDefaultModelResource{}
}

type AgentsDefaultModelResource struct {
	data *CoderdProviderData
}

func (r *AgentsDefaultModelResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.data.Client)
}

type AgentsDefaultModelResourceModel struct {
	ID             UUID `tfsdk:"id"`
	OrganizationID UUID `tfsdk:"organization_id"`
	ModelID        UUID `tfsdk:"model_id"`
}

func (r *AgentsDefaultModelResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents_default_model"
}

func (r *AgentsDefaultModelResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_agents_default_model is experimental. Changes are expected, and it is not recommended for production use.",
	)
}

func (r *AgentsDefaultModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"~> **Warning**\nThis resource is only compatible with Coder version [" + agentsDefaultModelMinVersion + "](https://github.com/coder/coder/releases/tag/v" + agentsDefaultModelMinVersion + ") and later.\n\n" +
			"Selects which `coderd_agents_model` is the default chat model for Coder Agents in an organization.\n\n" +
			"Coder enforces a single default model per organization: marking a model as default automatically demotes the " +
			"previous default in the same operation. Only one `coderd_agents_default_model` resource should exist per organization.\n\n" +
			"Destroying this resource does not clear the default server-side. Coder requires a default once models exist " +
			"and promotes a replacement when the current default is removed, so deleting this resource only stops " +
			"Terraform from managing which model is default.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Organization ID that identifies this organization's default Agents model selection.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					useStateForUnknownUnlessChanged("organization_id"),
				},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "Organization ID whose default Agents model is managed.",
				CustomType:          UUIDType,
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_id": schema.StringAttribute{
				MarkdownDescription: "ID of the `coderd_agents_model` to mark as the organization's default. Usually this is `coderd_agents_model.<name>.id`.",
				CustomType:          UUIDType,
				Required:            true,
			},
		},
	}
}

func (r *AgentsDefaultModelResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AgentsDefaultModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AgentsDefaultModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "setting default Agents model", map[string]any{
		"organization_id": plan.OrganizationID.ValueString(),
		"model_id":        plan.ModelID.ValueString(),
	})
	state, err := r.setDefault(ctx, plan.OrganizationID.ValueUUID(), plan.ModelID.ValueUUID())
	if err != nil {
		resp.Diagnostics.Append(r.agentsDefaultModelDiag(ctx, "set", plan.OrganizationID.ValueUUID(), plan.ModelID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AgentsDefaultModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AgentsDefaultModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	organizationID := state.OrganizationID.ValueUUID()
	configs, err := r.experimentalClient().ChatModels(ctx, organizationID)
	if err != nil {
		if isHTTPNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Organization %s not found or inaccessible. Marking its default Agents model selection as deleted.", organizationID))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read default Agents model, got error: %s", err))
		return
	}

	for _, config := range configs.Models {
		if config.IsDefault {
			state = stateFromAgentsDefaultModelConfig(config)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}

	// Coder requires a default whenever any models exist, so reaching here means
	// there are no models in this organization. Treat the selection as deleted.
	resp.Diagnostics.AddWarning("Client Warning",
		fmt.Sprintf("No default Agents model found among %d model config(s) in organization %s. Marking as deleted.", len(configs.Models), state.OrganizationID.ValueString()))
	resp.State.RemoveResource(ctx)
}

func (r *AgentsDefaultModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state AgentsDefaultModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "updating default Agents model", map[string]any{
		"organization_id": state.OrganizationID.ValueString(),
		"model_id":        plan.ModelID.ValueString(),
	})
	organizationID := state.OrganizationID.ValueUUID()
	updated, err := r.setDefault(ctx, organizationID, plan.ModelID.ValueUUID())
	if err != nil {
		resp.Diagnostics.Append(r.agentsDefaultModelDiag(ctx, "update", organizationID, plan.ModelID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *AgentsDefaultModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Coder requires a default once models exist and has no API for unsetting it.
	tflog.Info(ctx, "deleting coderd_agents_default_model is a no-op; Coder retains its current default model")
}

func (r *AgentsDefaultModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by organization ID. Read resolves the organization's current
	// default model without promoting or otherwise modifying any model.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), req.ID)...)
}

// setDefault marks the given model config as the default for its organization
// and returns the resulting resource state. The request carries only is_default;
// Coder merges it into the existing model config and atomically demotes the
// previous default in that organization.
func (r *AgentsDefaultModelResource) setDefault(ctx context.Context, organizationID, modelID uuid.UUID) (AgentsDefaultModelResourceModel, error) {
	updated, err := r.experimentalClient().UpdateChatModel(ctx, organizationID, modelID, codersdk.UpdateChatModelRequest{
		IsDefault: new(true),
	})
	if err != nil {
		return AgentsDefaultModelResourceModel{}, err
	}
	return stateFromAgentsDefaultModelConfig(updated), nil
}

// stateFromAgentsDefaultModelConfig maps the model config that Coder reports as the
// default into resource state. The organization UUID is the natural identity
// because each organization has at most one default model.
func stateFromAgentsDefaultModelConfig(config codersdk.ChatModel) AgentsDefaultModelResourceModel {
	return AgentsDefaultModelResourceModel{
		ID:             UUIDValue(config.OrganizationID),
		OrganizationID: UUIDValue(config.OrganizationID),
		ModelID:        UUIDValue(config.ID),
	}
}

func (r *AgentsDefaultModelResource) agentsDefaultModelDiag(ctx context.Context, action string, organizationID, modelID uuid.UUID, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	if !isHTTPNotFound(err) {
		diags.AddError("Client Error", fmt.Sprintf("Unable to %s the default Agents model, got error: %s", action, err))
		return diags
	}

	endpoint := fmt.Sprintf("/api/experimental/organizations/%s/chats/models/%s", organizationID, modelID)
	_, collectionErr := r.experimentalClient().ChatModels(ctx, organizationID)
	if collectionErr == nil {
		diags.AddError(
			"Default Agents Model Not Found or Inaccessible",
			fmt.Sprintf("Unable to %s the default Agents model: %s returned 404, but the organization's chat model collection is available. "+
				"Model %s does not exist in organization %s or is inaccessible. Original error: %s",
				action, endpoint, modelID, organizationID, err),
		)
		return diags
	}
	if !isHTTPNotFound(collectionErr) {
		diags.AddError(
			"Client Error",
			fmt.Sprintf("Unable to %s the default Agents model, and unable to determine whether the 404 from %s is model-specific because probing the organization's chat model collection failed. "+
				"Original error: %s. Collection probe error: %s",
				action, endpoint, err, collectionErr),
		)
		return diags
	}

	diags.AddError(
		"Organization Not Found or Inaccessible",
		fmt.Sprintf("Unable to %s the default Agents model: the chat model collection for organization %s returned 404. "+
			"The organization does not exist or is inaccessible. Original error: %s. Collection probe error: %s",
			action, organizationID, err, collectionErr),
	)
	return diags
}

func isHTTPNotFound(err error) bool {
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}
