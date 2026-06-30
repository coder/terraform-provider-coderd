package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// defaultAgentsModelID is the constant resource ID for the singleton default
// Agents model pointer. Coder enforces exactly one default chat model globally,
// so this resource has no scope key and uses a stable identifier instead.
const defaultAgentsModelID = "default"

var (
	_ resource.Resource                = &DefaultAgentsModelResource{}
	_ resource.ResourceWithConfigure   = &DefaultAgentsModelResource{}
	_ resource.ResourceWithImportState = &DefaultAgentsModelResource{}
	_ resource.ResourceWithModifyPlan  = &DefaultAgentsModelResource{}
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
		MarkdownDescription: "~> This resource is experimental. Changes are to be expected, and we recommend using it with caution in production environments.\n\n" +
			"Selects which `coderd_agents_model` is the deployment-wide default chat model for Coder Agents.\n\n" +
			"Coder enforces a single default model globally: marking a model as default automatically demotes the " +
			"previous default in the same operation. Because the default is a global singleton, only one " +
			"`coderd_default_agents_model` resource should exist per deployment.\n\n" +
			"Destroying this resource does not clear the default server-side. Coder always keeps exactly one model " +
			"marked as default and force-promotes a replacement when the current default is removed, so deleting this " +
			"resource only stops Terraform from managing which model is default.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Constant identifier for the singleton default Agents model pointer. Always `default`.",
				Computed:            true,
				Default:             stringdefault.StaticString(defaultAgentsModelID),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"model_id": schema.StringAttribute{
				MarkdownDescription: "ID of the `coderd_agents_model` to mark as the deployment-wide default. Usually this is `coderd_agents_model.<name>.id`.",
				CustomType:          UUIDType,
				Required:            true,
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

	tflog.Info(ctx, "setting default Agents model", map[string]any{"model_id": plan.ModelID.ValueString()})
	state, err := r.setDefault(ctx, plan.ModelID.ValueUUID())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to set default Agents model, got error: %s", err))
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

	configs, err := r.experimentalClient().ListChatModelConfigs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read default Agents model, got error: %s", err))
		return
	}

	for _, config := range configs {
		if config.IsDefault {
			state = stateFromDefaultModelConfig(config)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}

	// Coder keeps a default model whenever any model exists, so reaching here
	// means there are no models at all. Treat the pointer as deleted.
	resp.Diagnostics.AddWarning("Client Warning", "No default Agents model found. Marking as deleted.")
	resp.State.RemoveResource(ctx)
}

func (r *DefaultAgentsModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DefaultAgentsModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "updating default Agents model", map[string]any{"model_id": plan.ModelID.ValueString()})
	state, err := r.setDefault(ctx, plan.ModelID.ValueUUID())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update default Agents model, got error: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *DefaultAgentsModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// No-op. Coder always keeps exactly one model marked as default and
	// force-promotes a replacement when the current default is removed, so there
	// is nothing to clear server-side. Removing this resource only stops
	// Terraform from managing which model is default; the framework drops it from
	// state automatically.
	tflog.Info(ctx, "deleting coderd_default_agents_model is a no-op; Coder retains its current default model")
}

func (r *DefaultAgentsModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by the coderd_agents_model UUID to mark as default. Read then
	// reconciles model_id to whichever model Coder currently reports as default,
	// so the value self-corrects if the supplied ID is stale.
	resource.ImportStatePassthroughID(ctx, path.Root("model_id"), req, resp)
}

// setDefault marks the given model config as the deployment-wide default and
// returns the resulting resource state. The request carries only is_default;
// Coder merges it into the existing model config and atomically demotes the
// previous default.
func (r *DefaultAgentsModelResource) setDefault(ctx context.Context, modelID uuid.UUID) (DefaultAgentsModelResourceModel, error) {
	updated, err := r.experimentalClient().UpdateChatModelConfig(ctx, modelID, codersdk.UpdateChatModelConfigRequest{
		IsDefault: ptr.Ref(true),
	})
	if err != nil {
		return DefaultAgentsModelResourceModel{}, err
	}
	return stateFromDefaultModelConfig(updated), nil
}

// stateFromDefaultModelConfig maps the model config that Coder reports as the
// default into resource state. The resource ID is a constant because the default
// is a global singleton.
func stateFromDefaultModelConfig(config codersdk.ChatModelConfig) DefaultAgentsModelResourceModel {
	return DefaultAgentsModelResourceModel{
		ID:      types.StringValue(defaultAgentsModelID),
		ModelID: UUIDValue(config.ID),
	}
}
