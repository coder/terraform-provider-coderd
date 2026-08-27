package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
	"github.com/google/uuid"
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

// agentsModelMinVersion is the first Coder release that includes the
// organization-scoped chat model API.
const agentsModelMinVersion = "2.37.0"

var (
	_ resource.Resource                = &AgentsModelResource{}
	_ resource.ResourceWithConfigure   = &AgentsModelResource{}
	_ resource.ResourceWithImportState = &AgentsModelResource{}
	_ resource.ResourceWithModifyPlan  = &AgentsModelResource{}
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

// requireStateOrganizationID resolves organization_id from state. State
// written by provider versions that predate organization-scoped chat models
// lacks it, and Coder 2.37 removed the compatibility route that could recover
// it (coder/coder#28632), so such state must be re-imported.
func requireStateOrganizationID(value UUID, modelID uuid.UUID, diags *diag.Diagnostics) uuid.UUID {
	if value.IsNull() || value.IsUnknown() {
		diags.AddError(
			"Legacy Agents Model State",
			fmt.Sprintf(
				"State for Agents model %[1]s predates organization-scoped chat models and cannot be upgraded automatically. "+
					"Remove it from state and re-import it with the composite ID `<organization_id>/%[1]s`.",
				modelID,
			),
		)
		return uuid.Nil
	}
	return value.ValueUUID()
}

type AgentsModelResourceModel struct {
	ID                   UUID                   `tfsdk:"id"`
	OrganizationID       UUID                   `tfsdk:"organization_id"`
	AIProviderID         UUID                   `tfsdk:"ai_provider_id"`
	ProviderType         types.String           `tfsdk:"provider_type"`
	Model                types.String           `tfsdk:"model"`
	DisplayName          types.String           `tfsdk:"display_name"`
	Enabled              types.Bool             `tfsdk:"enabled"`
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

func (r *AgentsModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are to be expected, and we recommend using it with caution in production environments.\n\n" +
			"~> **Warning**\nThis resource is only compatible with Coder version [" + agentsModelMinVersion + "](https://github.com/coder/coder/releases/tag/v" + agentsModelMinVersion + ") and later.\n\n" +
			"Configures an organization-scoped, admin-managed chat model for Coder Agents, binding a model identifier to a configured AI provider (see `coderd_ai_provider`) along with context, compression, and optional JSON tuning settings. Import IDs use `<organization_id>/<id>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Agents model configuration ID.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "Organization ID that owns the Agents model configuration. Defaults to the provider default organization ID.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
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
				MarkdownDescription: "Optional JSON blob of per-call tuning for the model, such as `max_output_tokens`, `temperature`, `top_p`, and `provider_options`. See the field reference (including per-provider `provider_options`) at https://pkg.go.dev/github.com/coder/coder/v2/codersdk#ChatModelCallConfig.",
				CustomType:          agentsModelConfigType{},
				Optional:            true,
				Validators: []validator.String{
					agentsModelConfigNotEmptyValidator{},
					agentsModelConfigNoDroppedKeysValidator{},
				},
				PlanModifiers: []planmodifier.String{
					agentsModelConfigUseStateIfSemanticallyEqual{},
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
				// Deliberately NO UseStateForUnknown: unlike created_at, updated_at is
				// mutable. The server sets it to NOW() on every update, so pinning the
				// prior value makes a real update fail with "inconsistent result after
				// apply" whenever the update crosses a one-second boundary (the planned
				// timestamp != the server's fresh timestamp). The cosmetic post-refresh
				// "known after apply" on imported state is the correct tradeoff.
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
	if plan.OrganizationID.IsNull() || plan.OrganizationID.IsUnknown() {
		plan.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}
	createReq := plan.createRequest(&resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, "creating Agents model")
	organizationID := plan.OrganizationID.ValueUUID()
	modelConfig, err := r.createChatModelWithRetry(ctx, organizationID, createReq)
	if err != nil {
		resp.Diagnostics.Append(r.agentsModelCreateDiag(ctx, organizationID, err)...)
		return
	}

	providerType := r.lookupProviderType(ctx, modelConfig, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	state := stateFromModelConfig(modelConfig, providerType, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AgentsModelResource) lookupProviderType(ctx context.Context, config codersdk.ChatModel, diags *diag.Diagnostics) string {
	provider, err := r.data.Client.AIProvider(ctx, config.AIProviderID.String())
	if err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to read AI provider %s to derive provider_type, got error: %s", config.AIProviderID, err))
		return ""
	}
	return string(provider.Type)
}

// createChatModelWithRetry retries CreateChatModel on the 409
// default-election race. coder/coder#27968 now serializes default election
// server-side, so the retry is likely vestigial but harmless.
func (r *AgentsModelResource) createChatModelWithRetry(ctx context.Context, organizationID uuid.UUID, req codersdk.CreateChatModelRequest) (codersdk.ChatModel, error) {
	const maxAttempts = 10
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		config, err := r.experimentalClient().CreateChatModel(ctx, organizationID, req)
		if err == nil {
			return config, nil
		}
		var sdkErr *codersdk.Error
		if !errors.As(err, &sdkErr) || sdkErr.StatusCode() != http.StatusConflict {
			return codersdk.ChatModel{}, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return codersdk.ChatModel{}, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 100 * time.Millisecond):
		}
	}
	return codersdk.ChatModel{}, lastErr
}

func (r *AgentsModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AgentsModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	modelConfigID := state.ID.ValueUUID()
	organizationID := requireStateOrganizationID(state.OrganizationID, modelConfigID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	config, err := r.experimentalClient().ChatModel(ctx, organizationID, modelConfigID)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Agents model with ID %s not found. Marking as deleted.", modelConfigID.String()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read Agents model, got error: %s", err))
		return
	}

	providerType := r.lookupProviderType(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	refreshed := stateFromModelConfig(config, providerType, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
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
	organizationID := requireStateOrganizationID(state.OrganizationID, state.ID.ValueUUID(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	modelConfig, err := r.experimentalClient().UpdateChatModel(ctx, organizationID, state.ID.ValueUUID(), updateReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update Agents model, got error: %s", err))
		return
	}

	providerType := r.lookupProviderType(ctx, modelConfig, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated := stateFromModelConfig(modelConfig, providerType, &resp.Diagnostics)
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
	organizationID := requireStateOrganizationID(state.OrganizationID, state.ID.ValueUUID(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.experimentalClient().DeleteChatModel(ctx, organizationID, state.ID.ValueUUID()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete Agents model, got error: %s", err))
		return
	}
}

func (r *AgentsModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid Import ID", "Expected `<organization_id>/<id>`.")
		return
	}
	organizationID, err := uuid.Parse(parts[0])
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Unable to parse organization ID as UUID: %s", err))
		return
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Unable to parse Agents model ID as UUID: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID.String())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id.String())...)
}

func (r *AgentsModelResource) agentsModelCreateDiag(ctx context.Context, organizationID uuid.UUID, createErr error) diag.Diagnostics {
	var diags diag.Diagnostics

	var sdkErr *codersdk.Error
	if !errors.As(createErr, &sdkErr) || sdkErr.StatusCode() != http.StatusNotFound {
		diags.AddError("Client Error", fmt.Sprintf("Unable to create Agents model, got error: %s", createErr))
		return diags
	}

	createEndpoint := fmt.Sprintf("/api/v2/organizations/%s/chats/models", organizationID)
	probeEndpoint := fmt.Sprintf("/api/v2/organizations/%s/chats/models", r.data.DefaultOrganizationID)
	_, probeErr := r.experimentalClient().ChatModels(ctx, r.data.DefaultOrganizationID)
	if probeErr == nil {
		diags.AddError(
			"Invalid Agents Model Organization",
			fmt.Sprintf("Unable to create the Agents model: the deployment returned 404 for %s, but the same endpoint is available for the provider's default organization. "+
				"Organization %s may not exist or the token may not have access to it. Original error: %s",
				createEndpoint, organizationID, createErr),
		)
		return diags
	}

	var probeSDKErr *codersdk.Error
	if errors.As(probeErr, &probeSDKErr) && probeSDKErr.StatusCode() == http.StatusNotFound {
		diags.AddError(
			"Agents Model Endpoint Unavailable",
			fmt.Sprintf("Unable to create the Agents model: the deployment returned 404 for %s, and the capability probe returned 404 for %s. "+
				"This resource requires Coder version %s or later; upgrade the deployment, or remove "+
				"`coderd_agents_model` from your configuration. Original error: %s. Probe error: %s",
				createEndpoint, probeEndpoint, agentsModelMinVersion, createErr, probeErr),
		)
		return diags
	}

	diags.AddError(
		"Client Error",
		fmt.Sprintf("Unable to create the Agents model, and unable to determine whether the endpoint is supported because the capability probe for %s also failed. Original error: %s. Probe error: %s",
			probeEndpoint, createErr, probeErr),
	)
	return diags
}

func (m AgentsModelResourceModel) createRequest(diags *diag.Diagnostics) codersdk.CreateChatModelRequest {
	aiProviderID := m.AIProviderID.ValueUUID()
	req := codersdk.CreateChatModelRequest{
		AIProviderID:         &aiProviderID,
		Model:                m.Model.ValueString(),
		DisplayName:          m.DisplayName.ValueString(),
		Enabled:              new(m.Enabled.ValueBool()),
		ContextLimit:         new(m.ContextLimit.ValueInt64()),
		CompressionThreshold: new(int32(m.CompressionThreshold.ValueInt64())),
		ModelConfig:          agentsModelDecodeConfig(m.ModelConfig, diags),
	}
	return req
}

func (m AgentsModelResourceModel) updateRequest(state AgentsModelResourceModel, diags *diag.Diagnostics) codersdk.UpdateChatModelRequest {
	var req codersdk.UpdateChatModelRequest
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
		req.Enabled = new(m.Enabled.ValueBool())
	}
	if !m.ContextLimit.Equal(state.ContextLimit) {
		req.ContextLimit = new(m.ContextLimit.ValueInt64())
	}
	if !m.CompressionThreshold.Equal(state.CompressionThreshold) {
		req.CompressionThreshold = new(int32(m.CompressionThreshold.ValueInt64()))
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

func stateFromModelConfig(config codersdk.ChatModel, providerType string, diags *diag.Diagnostics) AgentsModelResourceModel {
	return AgentsModelResourceModel{
		ID:                   UUIDValue(config.ID),
		OrganizationID:       UUIDValue(config.OrganizationID),
		AIProviderID:         UUIDValue(config.AIProviderID),
		ProviderType:         types.StringValue(providerType),
		Model:                types.StringValue(config.Model),
		DisplayName:          types.StringValue(config.DisplayName),
		Enabled:              types.BoolValue(config.Enabled),
		ContextLimit:         types.Int64Value(config.ContextLimit),
		CompressionThreshold: types.Int64Value(int64(config.CompressionThreshold)),
		ModelConfig:          agentsModelConfigToState(config.ModelConfig, diags),
		CreatedAt:            types.Int64Value(config.CreatedAt.Unix()),
		UpdatedAt:            types.Int64Value(config.UpdatedAt.Unix()),
	}
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
	// Sort keys alphabetically so the stored value matches the user's jsonencode
	// config byte-for-byte; otherwise every post-import plan spuriously marks
	// updated_at unknown (see agentsModelConfigSortedJSON).
	sorted, err := agentsModelConfigSortedJSON(encoded)
	if err != nil {
		diags.AddError("Model Config Error", fmt.Sprintf("Unable to normalize returned model_config: %s", err))
		return newAgentsModelConfigNull()
	}
	return newAgentsModelConfigValue(sorted)
}
