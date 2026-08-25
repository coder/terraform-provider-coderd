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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// defaultAgentsModelMinVersion is the first Coder release that can include the
// organization-scoped chat model API.
const defaultAgentsModelMinVersion = "2.37.0"

var (
	_ resource.Resource                 = &DefaultAgentsModelResource{}
	_ resource.ResourceWithConfigure    = &DefaultAgentsModelResource{}
	_ resource.ResourceWithImportState  = &DefaultAgentsModelResource{}
	_ resource.ResourceWithModifyPlan   = &DefaultAgentsModelResource{}
	_ resource.ResourceWithUpgradeState = &DefaultAgentsModelResource{}
)

func NewDefaultAgentsModelResource() resource.Resource {
	return &DefaultAgentsModelResource{}
}

type DefaultAgentsModelResource struct {
	data *CoderdProviderData
}

func (r *DefaultAgentsModelResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.data.Client)
}

type DefaultAgentsModelResourceModel struct {
	ID             UUID `tfsdk:"id"`
	OrganizationID UUID `tfsdk:"organization_id"`
	ModelID        UUID `tfsdk:"model_id"`
}

// defaultAgentsModelResourceModelV0 is the model for schema version 0, when the
// resource tracked the deployment-wide default under the constant id "default"
// with no organization.
type defaultAgentsModelResourceModelV0 struct {
	ID      types.String `tfsdk:"id"`
	ModelID UUID         `tfsdk:"model_id"`
}

func (r *DefaultAgentsModelResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_default_agents_model"
}

func (r *DefaultAgentsModelResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_default_agents_model is experimental. Changes are expected, and it is not recommended for production use.",
	)
}

func (r *DefaultAgentsModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// Version 1 replaced the constant id "default" (schema version 0) with
		// the organization UUID when defaults became per-organization.
		Version: 1,
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"~> **Warning**\nThis resource is only compatible with Coder version [" + defaultAgentsModelMinVersion + "](https://github.com/coder/coder/releases/tag/v" + defaultAgentsModelMinVersion + ") and later.\n\n" +
			"Selects which `coderd_agents_model` is the default chat model for Coder Agents in an organization.\n\n" +
			"Coder enforces a single default model per organization: marking a model as default automatically demotes the " +
			"previous default in the same operation. Only one `coderd_default_agents_model` resource should exist per organization.\n\n" +
			"Destroying this resource does not clear the default server-side. Coder requires a default once models exist " +
			"and promotes a replacement when the current default is removed, so deleting this resource only stops " +
			"Terraform from managing which model is default.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Organization ID that identifies this organization's default Agents model selection.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
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

// defaultAgentsModelSchemaV0 is the resource schema at version 0 (provider
// <= 0.0.23), when the default was deployment-wide: id held the constant
// string "default" and there was no organization_id. Only the attribute types
// matter here; id must be a plain string because "default" is not a UUID.
func defaultAgentsModelSchemaV0() schema.Schema {
	return schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
			},
			"model_id": schema.StringAttribute{
				CustomType: UUIDType,
				Required:   true,
			},
		},
	}
}

func (r *DefaultAgentsModelResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	priorSchema := defaultAgentsModelSchemaV0()
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &priorSchema,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior defaultAgentsModelResourceModelV0
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				// Leave id and organization_id null rather than resolving them
				// here: Read and Update already treat a null organization_id as
				// legacy state and recover the organization the v0 resource
				// managed (Coder's default organization) through the
				// compatibility route, rewriting both fields.
				resp.Diagnostics.Append(resp.State.Set(ctx, &DefaultAgentsModelResourceModel{
					ID:             NewUUIDNull(),
					OrganizationID: NewUUIDNull(),
					ModelID:        prior.ModelID,
				})...)
			},
		},
	}
}

func (r *DefaultAgentsModelResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DefaultAgentsModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan DefaultAgentsModelResourceModel
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
		resp.Diagnostics.Append(defaultAgentsModelDiag("set", plan.OrganizationID.ValueUUID(), plan.ModelID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DefaultAgentsModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state DefaultAgentsModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configs codersdk.OrganizationChatModelsResponse
	var err error
	if state.OrganizationID.IsNull() || state.OrganizationID.IsUnknown() {
		// Legacy state predates organization_id. The old unscoped resource managed
		// models in Coder's default organization, which may differ from the
		// provider user's first organization.
		configs, err = legacyDefaultOrganizationChatModels(ctx, r.data.Client)
	} else {
		configs, err = r.experimentalClient().ChatModels(ctx, state.OrganizationID.ValueUUID())
	}
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read default Agents model, got error: %s", err))
		return
	}

	for _, config := range configs.Models {
		if config.IsDefault {
			state = stateFromDefaultModelConfig(config)
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

func (r *DefaultAgentsModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state DefaultAgentsModelResourceModel
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
	if state.OrganizationID.IsNull() || state.OrganizationID.IsUnknown() {
		config, found, err := legacyDefaultOrganizationChatModel(ctx, r.data.Client, plan.ModelID.ValueUUID())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to resolve the organization for legacy default Agents model state, got error: %s", err))
			return
		}
		if !found {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update default Agents model because model %s no longer exists.", plan.ModelID.ValueString()))
			return
		}
		organizationID = config.OrganizationID
	}
	updated, err := r.setDefault(ctx, organizationID, plan.ModelID.ValueUUID())
	if err != nil {
		resp.Diagnostics.Append(defaultAgentsModelDiag("update", organizationID, plan.ModelID.ValueUUID(), err)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *DefaultAgentsModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Coder requires a default once models exist and has no API for unsetting it.
	tflog.Info(ctx, "deleting coderd_default_agents_model is a no-op; Coder retains its current default model")
}

func (r *DefaultAgentsModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by organization ID. Read resolves the organization's current
	// default model without promoting or otherwise modifying any model.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), req.ID)...)
}

// setDefault marks the given model config as the default for its organization
// and returns the resulting resource state. The request carries only is_default;
// Coder merges it into the existing model config and atomically demotes the
// previous default in that organization.
func (r *DefaultAgentsModelResource) setDefault(ctx context.Context, organizationID, modelID uuid.UUID) (DefaultAgentsModelResourceModel, error) {
	updated, err := r.experimentalClient().UpdateChatModel(ctx, organizationID, modelID, codersdk.UpdateChatModelRequest{
		IsDefault: new(true),
	})
	if err != nil {
		return DefaultAgentsModelResourceModel{}, err
	}
	return stateFromDefaultModelConfig(updated), nil
}

// stateFromDefaultModelConfig maps the model config that Coder reports as the
// default into resource state. The organization UUID is the natural identity
// because each organization has at most one default model.
func stateFromDefaultModelConfig(config codersdk.ChatModel) DefaultAgentsModelResourceModel {
	return DefaultAgentsModelResourceModel{
		ID:             UUIDValue(config.OrganizationID),
		OrganizationID: UUIDValue(config.OrganizationID),
		ModelID:        UUIDValue(config.ID),
	}
}

func defaultAgentsModelDiag(action string, organizationID, modelID uuid.UUID, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		endpoint := fmt.Sprintf("/api/experimental/organizations/%s/chats/models/%s", organizationID, modelID)
		diags.AddError(
			"Unsupported Coder Version",
			fmt.Sprintf("Unable to %s the default Agents model: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later; upgrade the deployment, or remove "+
				"`coderd_default_agents_model` from your configuration. Original error: %s",
				action, endpoint, defaultAgentsModelMinVersion, err),
		)
		return diags
	}

	diags.AddError("Client Error", fmt.Sprintf("Unable to %s the default Agents model, got error: %s", action, err))
	return diags
}
