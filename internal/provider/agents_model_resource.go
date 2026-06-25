package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource                   = &AgentsModelResource{}
	_ resource.ResourceWithConfigure      = &AgentsModelResource{}
	_ resource.ResourceWithImportState    = &AgentsModelResource{}
	_ resource.ResourceWithModifyPlan     = &AgentsModelResource{}
	_ resource.ResourceWithValidateConfig = &AgentsModelResource{}
)

func NewAgentsModelResource() resource.Resource {
	return &AgentsModelResource{}
}

type AgentsModelResource struct {
	data *CoderdProviderData
}

func (r *AgentsModelResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.data.Client)
}

type AgentsModelResourceModel struct {
	ID                   UUID                   `tfsdk:"id"`
	AIProviderID         UUID                   `tfsdk:"ai_provider_id"`
	ProviderType         types.String           `tfsdk:"provider_type"`
	Model                types.String           `tfsdk:"model"`
	DisplayName          types.String           `tfsdk:"display_name"`
	Enabled              types.Bool             `tfsdk:"enabled"`
	IsDefault            types.Bool             `tfsdk:"is_default"`
	ContextLimit         types.Int64            `tfsdk:"context_limit"`
	CompressionThreshold types.Int64            `tfsdk:"compression_threshold"`
	ModelConfig          agentsModelConfigValue `tfsdk:"model_config"`
	CreatedAt            types.Int64            `tfsdk:"created_at"`
	UpdatedAt            types.Int64            `tfsdk:"updated_at"`
}

func (r *AgentsModelResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents_model"
}

func (r *AgentsModelResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_agents_model is experimental. Changes are expected, and it is not recommended for production use.",
	)
}

func (r *AgentsModelResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data AgentsModelResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !data.IsDefault.IsNull() && !data.IsDefault.IsUnknown() && !data.IsDefault.ValueBool() {
		resp.Diagnostics.AddAttributeError(
			path.Root("is_default"),
			"Invalid is_default",
			"Coder elects the default model server-side. Set is_default = true on one model and omit it on others.",
		)
	}
}

func (r *AgentsModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are to be expected, and we recommend using it with caution in production environments.\n\n" +
			"Configures an admin-managed chat model for Coder Agents, binding a model identifier to a configured AI provider (see `coderd_ai_provider`) along with context, compression, default election, and optional JSON tuning settings.\n\n" +
			"The server owns default election: set `is_default = true` on at most one model and omit it on the others rather than forcing it to false.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Agents model configuration ID.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"ai_provider_id": schema.StringAttribute{
				MarkdownDescription: "AI provider ID that backs this model. Usually this is `coderd_ai_provider.<name>.id`. Updating it re-derives the read-only `provider_type` from the referenced provider.",
				CustomType:          UUIDType,
				Required:            true,
			},
			"provider_type": schema.StringAttribute{
				MarkdownDescription: "Provider type derived by Coder from `ai_provider_id`, for example `openai`, `anthropic`, or `bedrock`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					useStateForUnknownUnlessChanged("ai_provider_id"),
				},
			},
			"model": schema.StringAttribute{
				MarkdownDescription: "Model identifier to use with the referenced provider.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name shown in Coder.",
				Optional:            true,
				Computed:            true,
				// Reject "" since Coder ignores a blank update and keeps the prior value, causing drift.
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					codersdkvalidator.DisplayName(),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether this model configuration is enabled. Defaults to true.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether this is the default model for new chats. Coder manages the single default server-side, so set `is_default = true` on one model and omit it on others.",
				Optional:            true,
				Computed:            true,
			},
			"context_limit": schema.Int64Attribute{
				MarkdownDescription: "Maximum context window for this model. Must be greater than zero.",
				Required:            true,
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"compression_threshold": schema.Int64Attribute{
				MarkdownDescription: "Percentage of the context window at which Coder should compact chat context. Defaults to 70 and must be between 0 and 100.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(70),
				Validators: []validator.Int64{
					int64validator.Between(0, 100),
				},
			},
			// JSON, not typed attributes: ChatModelCallConfig is large, evolving,
			// and its provider_options is a tagged union Terraform can't express.
			"model_config": schema.StringAttribute{
				MarkdownDescription: "Optional JSON blob of per-call tuning for the model, such as `max_output_tokens`, `temperature`, `top_p`, `cost`, and `provider_options`. See the field reference (including per-provider `provider_options`) at https://pkg.go.dev/github.com/coder/coder/v2/codersdk#ChatModelCallConfig.",
				CustomType:          agentsModelConfigType{},
				Optional:            true,
				Validators: []validator.String{
					agentsModelConfigNotEmptyValidator{},
				},
			},
			"created_at": schema.Int64Attribute{
				MarkdownDescription: "Creation timestamp as Unix seconds.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.Int64Attribute{
				MarkdownDescription: "Last update timestamp as Unix seconds.",
				Computed:            true,
			},
		},
	}
}

func (r *AgentsModelResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AgentsModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AgentsModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	createReq := plan.createRequest(&resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "creating Agents model")
	modelConfig, err := r.experimentalClient().CreateChatModelConfig(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create Agents model, got error: %s", err))
		return
	}

	state := stateFromModelConfig(modelConfig, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AgentsModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AgentsModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	modelConfigID := state.ID.ValueUUID()
	configs, err := r.experimentalClient().ListChatModelConfigs(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read Agents model, got error: %s", err))
		return
	}

	for _, config := range configs {
		if config.ID == modelConfigID {
			refreshed := stateFromModelConfig(config, &resp.Diagnostics)
			if resp.Diagnostics.HasError() {
				return
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
			return
		}
	}

	resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Agents model with ID %s not found. Marking as deleted.", modelConfigID.String()))
	resp.State.RemoveResource(ctx)
}

func (r *AgentsModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state AgentsModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updateReq := plan.updateRequest(state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "updating Agents model", map[string]any{"id": state.ID.ValueString()})
	modelConfig, err := r.experimentalClient().UpdateChatModelConfig(ctx, state.ID.ValueUUID(), updateReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update Agents model, got error: %s", err))
		return
	}

	updated := stateFromModelConfig(modelConfig, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *AgentsModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AgentsModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "deleting Agents model", map[string]any{"id": state.ID.ValueString()})
	if err := r.experimentalClient().DeleteChatModelConfig(ctx, state.ID.ValueUUID()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete Agents model, got error: %s", err))
		return
	}
}

func (r *AgentsModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m AgentsModelResourceModel) createRequest(diags *diag.Diagnostics) codersdk.CreateChatModelConfigRequest {
	aiProviderID := m.AIProviderID.ValueUUID()
	req := codersdk.CreateChatModelConfigRequest{
		AIProviderID:         &aiProviderID,
		Model:                m.Model.ValueString(),
		DisplayName:          m.DisplayName.ValueString(),
		Enabled:              ptr.Ref(m.Enabled.ValueBool()),
		ContextLimit:         ptr.Ref(m.ContextLimit.ValueInt64()),
		CompressionThreshold: ptr.Ref(int32(m.CompressionThreshold.ValueInt64())),
		ModelConfig:          agentsModelDecodeConfig(m.ModelConfig, diags),
	}
	// Omitted is_default is unknown (no static default); leave it nil so Coder elects the default.
	if !m.IsDefault.IsUnknown() {
		req.IsDefault = m.IsDefault.ValueBoolPointer()
	}
	return req
}

func (m AgentsModelResourceModel) updateRequest(state AgentsModelResourceModel, diags *diag.Diagnostics) codersdk.UpdateChatModelConfigRequest {
	var req codersdk.UpdateChatModelConfigRequest
	if !m.AIProviderID.Equal(state.AIProviderID) {
		aiProviderID := m.AIProviderID.ValueUUID()
		req.AIProviderID = &aiProviderID
	}
	if !m.Model.Equal(state.Model) {
		req.Model = m.Model.ValueString()
	}
	if !m.DisplayName.Equal(state.DisplayName) {
		req.DisplayName = m.DisplayName.ValueString()
	}
	if !m.Enabled.Equal(state.Enabled) {
		req.Enabled = ptr.Ref(m.Enabled.ValueBool())
	}
	if !m.IsDefault.IsNull() && !m.IsDefault.IsUnknown() && !m.IsDefault.Equal(state.IsDefault) {
		req.IsDefault = m.IsDefault.ValueBoolPointer()
	}
	if !m.ContextLimit.Equal(state.ContextLimit) {
		req.ContextLimit = ptr.Ref(m.ContextLimit.ValueInt64())
	}
	if !m.CompressionThreshold.Equal(state.CompressionThreshold) {
		req.CompressionThreshold = ptr.Ref(int32(m.CompressionThreshold.ValueInt64()))
	}
	if !m.ModelConfig.Equal(state.ModelConfig) {
		if m.ModelConfig.IsNull() {
			// Send an empty object so Coder clears the stored tuning config.
			req.ModelConfig = &codersdk.ChatModelCallConfig{}
		} else {
			req.ModelConfig = agentsModelDecodeConfig(m.ModelConfig, diags)
		}
	}
	return req
}

func stateFromModelConfig(config codersdk.ChatModelConfig, diags *diag.Diagnostics) AgentsModelResourceModel {
	out := AgentsModelResourceModel{
		ID:                   UUIDValue(config.ID),
		ProviderType:         types.StringValue(config.Provider),
		Model:                types.StringValue(config.Model),
		DisplayName:          types.StringValue(config.DisplayName),
		Enabled:              types.BoolValue(config.Enabled),
		IsDefault:            types.BoolValue(config.IsDefault),
		ContextLimit:         types.Int64Value(config.ContextLimit),
		CompressionThreshold: types.Int64Value(int64(config.CompressionThreshold)),
		ModelConfig:          agentsModelConfigToState(config.ModelConfig, diags),
		CreatedAt:            types.Int64Value(config.CreatedAt.Unix()),
		UpdatedAt:            types.Int64Value(config.UpdatedAt.Unix()),
	}
	if config.AIProviderID != nil {
		out.AIProviderID = UUIDValue(*config.AIProviderID)
	} else {
		out.AIProviderID = NewUUIDNull()
	}
	return out
}

// agentsModelDecodeConfig decodes the model_config JSON string into the SDK
// type. Null or unknown values become nil so the field is omitted from the
// request and Coder keeps its existing value.
func agentsModelDecodeConfig(v agentsModelConfigValue, diags *diag.Diagnostics) *codersdk.ChatModelCallConfig {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	var config codersdk.ChatModelCallConfig
	if err := json.Unmarshal([]byte(v.ValueString()), &config); err != nil {
		diags.AddAttributeError(path.Root("model_config"), "Invalid Model Config", fmt.Sprintf("Unable to decode `model_config`: %s", err))
		return nil
	}
	return &config
}

// agentsModelConfigToState serializes the model_config returned by Coder back
// into a normalized JSON string. Coder returns null when no tuning config is
// set, which maps to a null attribute.
func agentsModelConfigToState(remote *codersdk.ChatModelCallConfig, diags *diag.Diagnostics) agentsModelConfigValue {
	if remote == nil {
		return newAgentsModelConfigNull()
	}
	encoded, err := json.Marshal(remote)
	if err != nil {
		diags.AddError("Model Config Error", fmt.Sprintf("Unable to encode returned model_config: %s", err))
		return newAgentsModelConfigNull()
	}
	return newAgentsModelConfigValue(string(encoded))
}
