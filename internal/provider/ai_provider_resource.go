package provider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	bedrockCanonicalBaseURLRegex = regexp.MustCompile(`(?i)^https://bedrock-runtime\.([a-z0-9-]+)\.amazonaws\.com/?$`)

	_ resource.Resource                   = &AIProviderResource{}
	_ resource.ResourceWithImportState    = &AIProviderResource{}
	_ resource.ResourceWithValidateConfig = &AIProviderResource{}
	_ resource.ResourceWithModifyPlan     = &AIProviderResource{}

	_ planmodifier.String = bedrockRegionPlanModifier{}
)

func NewAIProviderResource() resource.Resource {
	return &AIProviderResource{}
}

type AIProviderResource struct {
	data *CoderdProviderData
}

type AIProviderResourceModel struct {
	ID              UUID                     `tfsdk:"id"`
	Type            types.String             `tfsdk:"type"`
	Name            types.String             `tfsdk:"name"`
	DisplayName     types.String             `tfsdk:"display_name"`
	Enabled         types.Bool               `tfsdk:"enabled"`
	BaseURL         types.String             `tfsdk:"base_url"`
	APIKeyWO        types.String             `tfsdk:"api_key_wo"`
	APIKeyWOVersion types.Int64              `tfsdk:"api_key_wo_version"`
	APIKeyMasked    types.String             `tfsdk:"api_key_masked"`
	Settings        *AIProviderSettingsModel `tfsdk:"settings"`
	CreatedAt       types.Int64              `tfsdk:"created_at"`
	UpdatedAt       types.Int64              `tfsdk:"updated_at"`
}

type AIProviderSettingsModel struct {
	Bedrock *AIProviderBedrockSettingsModel `tfsdk:"bedrock"`
}

type AIProviderBedrockSettingsModel struct {
	Region               types.String `tfsdk:"region"`
	Model                types.String `tfsdk:"model"`
	SmallFastModel       types.String `tfsdk:"small_fast_model"`
	RoleARN              types.String `tfsdk:"role_arn"`
	ExternalID           types.String `tfsdk:"external_id"`
	AccessKeyWO          types.String `tfsdk:"access_key_wo"`
	AccessKeySecretWO    types.String `tfsdk:"access_key_secret_wo"`
	CredentialsWOVersion types.Int64  `tfsdk:"credentials_wo_version"`
}

func (r *AIProviderResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ai_provider"
}

func (r *AIProviderResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_ai_provider is experimental. Changes are expected, and it is not recommended for production use.",
	)
}

func (r *AIProviderResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"-> `_wo` attributes are [write-only](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments): " +
			"their values are sent to Coder but never stored in Terraform state. This resource therefore requires Terraform 1.11 or later.\n\n" +
			"Configures an AI Provider for use with Coder's AI Gateway & Coder Agents.\n\n" +
			"For `type = \"bedrock\"`, omit `settings.bedrock.access_key_wo` and `settings.bedrock.access_key_secret_wo` to use the AWS SDK default credential chain as resolved by the Coder server process (IAM role, IRSA, environment variables, shared config, SSO, IMDS, and more). Set both together to use static IAM-user credentials.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "AI provider ID.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "AI provider type. Valid values are `openai`, `anthropic`, `azure`, `bedrock`, `google`, `openai-compat`, `openrouter`, `vercel`, and `copilot`.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(codersdk.AIProviderTypeOpenAI),
						string(codersdk.AIProviderTypeAnthropic),
						string(codersdk.AIProviderTypeAzure),
						string(codersdk.AIProviderTypeBedrock),
						string(codersdk.AIProviderTypeGoogle),
						string(codersdk.AIProviderTypeOpenAICompat),
						string(codersdk.AIProviderTypeOpenrouter),
						string(codersdk.AIProviderTypeVercel),
						string(codersdk.AIProviderTypeCopilot),
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Unique provider name. Must be lowercase alphanumeric words separated by hyphens.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(codersdk.AIProviderNameRegex, "must be lowercase alphanumeric words separated by hyphens"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name shown in Coder. If omitted, defaults to the provider name.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether this AI provider is enabled. Defaults to true.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"base_url": schema.StringAttribute{
				MarkdownDescription: "Absolute HTTP(S) base URL for the upstream provider endpoint.",
				Required:            true,
			},
			"api_key_wo": schema.StringAttribute{
				MarkdownDescription: "Plaintext API key for the provider. Not valid for `bedrock` or `copilot`, or when `settings.bedrock` is set. Bump `api_key_wo_version` to rotate it.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.AlsoRequires(path.MatchRoot("api_key_wo_version")),
				},
			},
			"api_key_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version for the write-only API key. Required when `api_key_wo` is set; bump it whenever `api_key_wo` changes to rotate the stored key.",
				Optional:            true,
			},
			"api_key_masked": schema.StringAttribute{
				MarkdownDescription: "Masked API key value returned by Coder for display only.",
				Computed:            true,
			},
			"settings": schema.SingleNestedAttribute{
				MarkdownDescription: "Type-specific provider settings.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"bedrock": schema.SingleNestedAttribute{
						MarkdownDescription: "AWS Bedrock settings. Valid only for `type = \"bedrock\"` or `type = \"anthropic\"`.",
						Optional:            true,
						Attributes: map[string]schema.Attribute{
							"region": schema.StringAttribute{
								MarkdownDescription: "AWS region for Bedrock. If omitted, derived from the canonical Bedrock `base_url` attribute.",
								Optional:            true,
								Computed:            true,
								PlanModifiers: []planmodifier.String{
									bedrockRegionPlanModifier{},
								},
								Validators: []validator.String{
									// Coder drops empty strings on the wire (omitempty), so a
									// configured "" reads back as null and breaks the apply. Omit
									// the attribute instead of setting it empty.
									stringvalidator.LengthAtLeast(1),
								},
							},
							"model": schema.StringAttribute{
								MarkdownDescription: "Primary Bedrock model identifier.",
								Optional:            true,
								Computed:            true,
								PlanModifiers: []planmodifier.String{
									stringplanmodifier.UseStateForUnknown(),
								},
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"small_fast_model": schema.StringAttribute{
								MarkdownDescription: "Small/fast Bedrock model identifier used for background tasks.",
								Optional:            true,
								Computed:            true,
								PlanModifiers: []planmodifier.String{
									stringplanmodifier.UseStateForUnknown(),
								},
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"role_arn": schema.StringAttribute{
								MarkdownDescription: "ARN of an AWS IAM role to assume via STS before calling Bedrock. The base identity (the AWS SDK default credential chain or the static credentials) signs the AssumeRole call, and the temporary credentials sign Bedrock requests. Omit to call Bedrock with the base identity directly. Requires Coder v2.35.0 or later.",
								Optional:            true,
								Validators: []validator.String{
									stringvalidator.LengthAtLeast(1),
								},
							},
							"external_id": schema.StringAttribute{
								MarkdownDescription: "STS external ID the server generates and sends on the AssumeRole call when `role_arn` is set. Reference it in the assumed role's trust policy `sts:ExternalId` condition. Null until `role_arn` is first configured; stable afterwards. Requires Coder v2.36.0 or later.",
								Computed:            true,
								PlanModifiers: []planmodifier.String{
									bedrockExternalIDPlanModifier{},
								},
							},
							"access_key_wo": schema.StringAttribute{
								MarkdownDescription: "AWS access key ID for Bedrock. See [Coder's Amazon Bedrock provider docs](https://coder.com/docs/ai-coder/ai-gateway/providers#amazon-bedrock).",
								Optional:            true,
								Sensitive:           true,
								WriteOnly:           true,
								Validators: []validator.String{
									stringvalidator.AlsoRequires(
										path.MatchRoot("settings").AtName("bedrock").AtName("access_key_secret_wo"),
										path.MatchRoot("settings").AtName("bedrock").AtName("credentials_wo_version"),
									),
								},
							},
							"access_key_secret_wo": schema.StringAttribute{
								MarkdownDescription: "AWS secret access key for Bedrock.",
								Optional:            true,
								Sensitive:           true,
								WriteOnly:           true,
								Validators: []validator.String{
									stringvalidator.AlsoRequires(
										path.MatchRoot("settings").AtName("bedrock").AtName("access_key_wo"),
										path.MatchRoot("settings").AtName("bedrock").AtName("credentials_wo_version"),
									),
								},
							},
							"credentials_wo_version": schema.Int64Attribute{
								MarkdownDescription: "Version for Bedrock write-only credentials. Bump this value to send, rotate, or clear credentials.",
								Optional:            true,
							},
						},
					},
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

func (r *AIProviderResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AIProviderResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// The pointer-based model can't decode an unknown settings/bedrock object
	// (e.g. settings = var.x), so defer validation until those are known.
	var settings types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("settings"), &settings)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if settings.IsUnknown() {
		return
	}
	if !settings.IsNull() {
		var bedrock types.Object
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("settings").AtName("bedrock"), &bedrock)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if bedrock.IsUnknown() {
			return
		}
	}

	var data AIProviderResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	baseURL := ""
	baseURLKnown := !data.BaseURL.IsUnknown()
	if baseURLKnown {
		baseURL = data.BaseURL.ValueString()
		validateAIProviderBaseURL(resp.Diagnostics.AddAttributeError, path.Root("base_url"), baseURL)
	}

	if data.Type.IsUnknown() {
		return
	}
	providerType := codersdk.AIProviderType(data.Type.ValueString())

	isOpenAILike := providerType == codersdk.AIProviderTypeOpenAI || providerType == codersdk.AIProviderTypeOpenAICompat
	if parsed, err := url.Parse(strings.TrimSpace(baseURL)); baseURLKnown && isOpenAILike && err == nil && strings.Trim(parsed.Path, "/") == "" {
		resp.Diagnostics.AddAttributeWarning(
			path.Root("base_url"),
			"Base URL May Be Missing API Version",
			"`base_url` has no path segment. Coder sends requests to `<base_url>/chat/completions` without adding a "+
				"version prefix such as `/v1`, so a bare host targets `/chat/completions` at the root. Most "+
				"OpenAI-compatible endpoints require a version path, for example `https://api.openai.com/v1`.",
		)
	}

	if !data.APIKeyWO.IsNull() && !data.APIKeyWO.IsUnknown() {
		switch {
		case providerType == codersdk.AIProviderTypeBedrock || providerType == codersdk.AIProviderTypeCopilot:
			resp.Diagnostics.AddAttributeError(path.Root("api_key_wo"), "Invalid Attribute Combination", fmt.Sprintf("`api_key_wo` must not be configured when `type` is `%s`.", providerType))
		case data.bedrock() != nil:
			// The server rejects api_keys whenever settings.bedrock is set.
			resp.Diagnostics.AddAttributeError(path.Root("api_key_wo"), "Invalid Attribute Combination", "`api_key_wo` must not be configured when `settings.bedrock` is set; Bedrock-backed providers authenticate via `settings.bedrock`.")
		}
	}

	bedrock := data.bedrock()
	if bedrock == nil {
		switch {
		case providerType == codersdk.AIProviderTypeBedrock:
			resp.Diagnostics.AddAttributeError(path.Root("settings"), "Missing Bedrock Settings", "`type = \"bedrock\"` requires `settings.bedrock` with at least `region` or write-only AWS credentials.")
		case data.Settings != nil:
			// An empty settings = {} produces a null-vs-empty diff; reject it.
			resp.Diagnostics.AddAttributeError(path.Root("settings"), "Invalid Settings", "`settings` must include a `bedrock` block or be omitted.")
		}
		return
	}

	if providerType != codersdk.AIProviderTypeAnthropic && providerType != codersdk.AIProviderTypeBedrock {
		resp.Diagnostics.AddAttributeError(path.Root("settings").AtName("bedrock"), "Invalid Attribute Combination", "`settings.bedrock` is only valid when `type` is `anthropic` or `bedrock`.")
	}
	if providerType == codersdk.AIProviderTypeBedrock {
		if !baseURLKnown || bedrock.Region.IsUnknown() || bedrock.RoleARN.IsUnknown() || bedrock.AccessKeyWO.IsUnknown() || bedrock.AccessKeySecretWO.IsUnknown() {
			return
		}
		sdkSettings := codersdk.AIProviderBedrockSettings{
			Region:          bedrockRegion(baseURL, bedrock.Region, bedrock.Region),
			Model:           bedrock.Model.ValueString(),
			SmallFastModel:  bedrock.SmallFastModel.ValueString(),
			RoleARN:         bedrock.RoleARN.ValueString(),
			AccessKey:       stringPtrOrNil(bedrock.AccessKeyWO),
			AccessKeySecret: stringPtrOrNil(bedrock.AccessKeySecretWO),
		}
		if !sdkSettings.IsConfigured() {
			resp.Diagnostics.AddAttributeError(path.Root("settings").AtName("bedrock"), "Missing Bedrock Settings", "`type = \"bedrock\"` requires Bedrock settings sufficient for the Coder API: set `region` or write-only AWS credentials.")
		}
	}
}

func (r *AIProviderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config AIProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := plan.createRequest(config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if validations := createReq.Validate(); len(validations) > 0 {
		addValidationErrors(&resp.Diagnostics, validations)
		return
	}

	tflog.Info(ctx, "creating AI provider")
	provider, err := r.data.Client.CreateAIProvider(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create AI provider, got error: %s", err))
		return
	}
	checkBedrockRoleARNDropped(config, provider, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	state := plan.stateFromProvider(provider)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AIProviderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AIProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	idOrName := state.Name.ValueString()
	if !state.ID.IsNull() && !state.ID.IsUnknown() {
		idOrName = state.ID.ValueString()
	}
	provider, err := r.data.Client.AIProvider(ctx, idOrName)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("AI provider %s not found. Marking as deleted.", idOrName))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read AI provider, got error: %s", err))
		return
	}

	refreshed := state.stateFromProvider(provider)
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *AIProviderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config AIProviderResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patch := plan.updateRequest(state, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.validateEffectiveUpdateState(state, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if patch.IsEmpty() {
		// Nothing tracked changed; refresh from the server. The Coder
		// API rejects empty patches, so there is nothing to send.
		provider, err := r.data.Client.AIProvider(ctx, state.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read AI provider, got error: %s", err))
			return
		}
		refreshed := plan.stateFromProvider(provider)
		resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
		return
	}
	if validations := patch.Validate(); len(validations) > 0 {
		addValidationErrors(&resp.Diagnostics, validations)
		return
	}

	tflog.Info(ctx, "updating AI provider")
	provider, err := r.data.Client.UpdateAIProvider(ctx, state.ID.ValueString(), patch)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update AI provider, got error: %s", err))
		return
	}
	checkBedrockRoleARNDropped(config, provider, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated := plan.stateFromProvider(provider)
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *AIProviderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AIProviderResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	idOrName := state.Name.ValueString()
	if !state.ID.IsNull() && !state.ID.IsUnknown() {
		idOrName = state.ID.ValueString()
	}
	if err := r.data.Client.DeleteAIProvider(ctx, idOrName); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete AI provider, got error: %s", err))
	}
}

func (r *AIProviderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	provider, err := r.data.Client.AIProvider(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to import AI provider %q, got error: %s", req.ID, err))
		return
	}
	state := AIProviderResourceModel{}.stateFromProvider(provider)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (m AIProviderResourceModel) createRequest(config AIProviderResourceModel, diags *diag.Diagnostics) codersdk.CreateAIProviderRequest {
	var apiKeys []string
	if !config.APIKeyWO.IsNull() && !config.APIKeyWO.IsUnknown() {
		apiKeys = []string{config.APIKeyWO.ValueString()}
	}
	return codersdk.CreateAIProviderRequest{
		Type:        codersdk.AIProviderType(m.Type.ValueString()),
		Name:        m.Name.ValueString(),
		DisplayName: config.DisplayName.ValueString(),
		Enabled:     m.Enabled.ValueBool(),
		BaseURL:     m.BaseURL.ValueString(),
		APIKeys:     apiKeys,
		Settings:    m.sdkSettings(config, bedrockCredentialsConfigured(config.bedrock()), diags),
	}
}

func (m AIProviderResourceModel) updateRequest(state, config AIProviderResourceModel, diags *diag.Diagnostics) codersdk.UpdateAIProviderRequest {
	var patch codersdk.UpdateAIProviderRequest
	if !m.DisplayName.Equal(state.DisplayName) {
		v := config.DisplayName.ValueString()
		patch.DisplayName = &v
	}
	if !m.Enabled.Equal(state.Enabled) {
		v := m.Enabled.ValueBool()
		patch.Enabled = &v
	}
	if !m.BaseURL.Equal(state.BaseURL) {
		v := m.BaseURL.ValueString()
		patch.BaseURL = &v
	}

	// Send settings whenever they are (or were) present. The server merges
	// credentials, so omitting credential pointers leaves stored AWS keys
	// untouched; dropping the bedrock block clears settings server-side.
	credentialsChanged := credentialsVersionChanged(m.bedrock(), state.bedrock())
	if m.bedrock() != nil || state.bedrock() != nil {
		settings := m.sdkSettings(config, credentialsChanged, diags)
		patch.Settings = &settings
	}

	// Rotate the API key only when its version changes to a concrete value. A
	// null version preserves the stored key rather than clearing it.
	if !m.APIKeyWOVersion.IsNull() && !m.APIKeyWOVersion.Equal(state.APIKeyWOVersion) {
		if config.APIKeyWO.IsNull() || config.APIKeyWO.IsUnknown() {
			diags.AddAttributeError(path.Root("api_key_wo"), "Missing API Key", "`api_key_wo` must be configured when `api_key_wo_version` changes.")
		} else {
			patch.APIKeys = &[]codersdk.AIProviderKeyMutation{{APIKey: stringPtrOrNil(config.APIKeyWO)}}
		}
	}
	return patch
}

func (m AIProviderResourceModel) validateEffectiveUpdateState(state, config AIProviderResourceModel, diags *diag.Diagnostics) {
	if codersdk.AIProviderType(m.Type.ValueString()) != codersdk.AIProviderTypeBedrock {
		return
	}

	bedrock := m.bedrock()
	if bedrock == nil {
		diags.AddAttributeError(path.Root("settings"), "Missing Bedrock Settings", "`type = \"bedrock\"` requires `settings.bedrock`; it cannot be removed from an existing Bedrock provider.")
		return
	}

	if !credentialsVersionChanged(bedrock, state.bedrock()) {
		return
	}
	cfgBedrock := config.bedrock()
	if cfgBedrock == nil {
		return
	}
	settings := codersdk.AIProviderBedrockSettings{
		Region:          bedrockRegion(m.BaseURL.ValueString(), cfgBedrock.Region, bedrock.Region),
		RoleARN:         cfgBedrock.RoleARN.ValueString(),
		AccessKey:       stringPtrOrNil(cfgBedrock.AccessKeyWO),
		AccessKeySecret: stringPtrOrNil(cfgBedrock.AccessKeySecretWO),
	}
	if !settings.IsConfigured() {
		diags.AddAttributeError(path.Root("settings").AtName("bedrock"), "Missing Bedrock Settings", "`type = \"bedrock\"` requires Bedrock settings sufficient for the Coder API: set `region` or write-only AWS credentials.")
	}
}

func (m AIProviderResourceModel) sdkSettings(config AIProviderResourceModel, includeCredentials bool, diags *diag.Diagnostics) codersdk.AIProviderSettings {
	bedrock := m.bedrock()
	if bedrock == nil {
		return codersdk.AIProviderSettings{}
	}
	cfgBedrock := config.bedrock()
	cfgRegion := bedrock.Region
	if cfgBedrock != nil {
		cfgRegion = cfgBedrock.Region
	}
	settings := codersdk.AIProviderBedrockSettings{
		Region:         bedrockRegion(m.BaseURL.ValueString(), cfgRegion, bedrock.Region),
		Model:          bedrock.Model.ValueString(),
		SmallFastModel: bedrock.SmallFastModel.ValueString(),
		RoleARN:        bedrock.RoleARN.ValueString(),
	}
	if includeCredentials {
		if cfgBedrock == nil || cfgBedrock.AccessKeyWO.IsNull() || cfgBedrock.AccessKeyWO.IsUnknown() || cfgBedrock.AccessKeySecretWO.IsNull() || cfgBedrock.AccessKeySecretWO.IsUnknown() {
			diags.AddAttributeError(path.Root("settings").AtName("bedrock"), "Missing Bedrock Credentials", "Bedrock credential version changed, so both `access_key_wo` and `access_key_secret_wo` must be configured. Use empty strings for both to clear stored credentials.")
			return codersdk.AIProviderSettings{}
		}
		settings.AccessKey = stringPtrOrNil(cfgBedrock.AccessKeyWO)
		settings.AccessKeySecret = stringPtrOrNil(cfgBedrock.AccessKeySecretWO)
	}
	return codersdk.AIProviderSettings{Bedrock: &settings}
}

func (m AIProviderResourceModel) stateFromProvider(provider codersdk.AIProvider) AIProviderResourceModel {
	out := AIProviderResourceModel{
		ID:           UUIDValue(provider.ID),
		Type:         types.StringValue(string(provider.Type)),
		Name:         types.StringValue(provider.Name),
		DisplayName:  types.StringValue(provider.DisplayName),
		Enabled:      types.BoolValue(provider.Enabled),
		BaseURL:      types.StringValue(provider.BaseURL),
		CreatedAt:    types.Int64Value(provider.CreatedAt.Unix()),
		UpdatedAt:    types.Int64Value(provider.UpdatedAt.Unix()),
		APIKeyMasked: types.StringNull(),
		// Write-only value is not returned; version is Terraform-only.
		APIKeyWO:        types.StringNull(),
		APIKeyWOVersion: m.APIKeyWOVersion,
	}
	// This resource manages a single key and replaces all keys on rotation, so
	// len(APIKeys) is always 0 or 1; index 0 is the key we manage.
	if len(provider.APIKeys) > 0 {
		out.APIKeyMasked = types.StringValue(provider.APIKeys[0].Masked)
	}
	if provider.Settings.Bedrock != nil {
		out.Settings = &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:            stringValueOrNull(provider.Settings.Bedrock.Region),
			Model:             stringValueOrNull(provider.Settings.Bedrock.Model),
			SmallFastModel:    stringValueOrNull(provider.Settings.Bedrock.SmallFastModel),
			RoleARN:           stringValueOrNull(provider.Settings.Bedrock.RoleARN),
			ExternalID:        stringValueOrNull(provider.Settings.Bedrock.ExternalID),
			AccessKeyWO:       types.StringNull(),
			AccessKeySecretWO: types.StringNull(),
		}}
		if b := m.bedrock(); b != nil {
			out.Settings.Bedrock.CredentialsWOVersion = b.CredentialsWOVersion
		} else {
			out.Settings.Bedrock.CredentialsWOVersion = types.Int64Null()
		}
	}
	return out
}

// A Coder server older than v2.35.0 drops the unknown role_arn JSON key
// (omitempty), so a configured value round-trips to null and surfaces as a
// cryptic "inconsistent values for sensitive attribute" error. Fail loudly
// instead. (#387)
func checkBedrockRoleARNDropped(config AIProviderResourceModel, provider codersdk.AIProvider, diags *diag.Diagnostics) {
	b := config.bedrock()
	if b == nil || b.RoleARN.IsNull() || b.RoleARN.IsUnknown() || b.RoleARN.ValueString() == "" {
		return
	}
	if provider.Settings.Bedrock != nil && provider.Settings.Bedrock.RoleARN != "" {
		return
	}
	diags.AddAttributeError(
		path.Root("settings").AtName("bedrock").AtName("role_arn"),
		"Bedrock role_arn not supported by this Coder deployment",
		"The Coder server accepted the request but did not persist `role_arn`, which means it predates Bedrock STS assume-role support. Upgrade to Coder v2.35.0 or later, or remove `role_arn`.",
	)
}

func (m AIProviderResourceModel) bedrock() *AIProviderBedrockSettingsModel {
	if m.Settings == nil {
		return nil
	}
	return m.Settings.Bedrock
}

func bedrockRegion(baseURL string, configured, planned types.String) string {
	if !configured.IsNull() && !configured.IsUnknown() {
		return configured.ValueString()
	}
	if region := parseBedrockRegionFromBaseURL(baseURL); region != "" {
		return region
	}
	if !planned.IsNull() && !planned.IsUnknown() {
		return planned.ValueString()
	}
	return ""
}

func parseBedrockRegionFromBaseURL(baseURL string) string {
	match := bedrockCanonicalBaseURLRegex.FindStringSubmatch(strings.TrimSpace(baseURL))
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(match[1])
}

// bedrockRegionPlanModifier derives the planned Bedrock region from base_url
// when region is not explicitly configured.
type bedrockRegionPlanModifier struct{}

func (bedrockRegionPlanModifier) Description(_ context.Context) string {
	return "Derives the Bedrock region from base_url when region is not explicitly set."
}

func (m bedrockRegionPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (bedrockRegionPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// User set region explicitly: keep their value.
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() {
		return
	}

	var baseURL types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("base_url"), &baseURL)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// base_url not yet known: region resolves after apply.
	if baseURL.IsUnknown() {
		resp.PlanValue = types.StringUnknown()
		return
	}
	// Canonical Bedrock URL: derive the region so plan matches apply.
	if region := parseBedrockRegionFromBaseURL(baseURL.ValueString()); region != "" {
		resp.PlanValue = types.StringValue(region)
		return
	}
	// Otherwise the region is stable across this change: preserve prior state.
	if !req.StateValue.IsNull() {
		resp.PlanValue = req.StateValue
	}
}

// bedrockExternalIDPlanModifier plans the server-generated STS external ID:
// created when role_arn is first set, then kept even if role_arn is cleared.
type bedrockExternalIDPlanModifier struct{}

func (bedrockExternalIDPlanModifier) Description(_ context.Context) string {
	return "Preserves the server-generated external ID; plans a new one only when role_arn is first set."
}

func (m bedrockExternalIDPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (bedrockExternalIDPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Once stored, the ID never changes.
	if !req.StateValue.IsNull() {
		resp.PlanValue = req.StateValue
		return
	}

	var roleARN types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("settings").AtName("bedrock").AtName("role_arn"), &roleARN)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unknown or newly set role_arn: stays unknown, the server decides on apply.
	if roleARN.IsUnknown() {
		return
	}
	if !roleARN.IsNull() && roleARN.ValueString() != "" {
		return
	}
	// No role and none stored: no ID.
	resp.PlanValue = types.StringNull()
}

func validateAIProviderBaseURL(addError func(path.Path, string, string), attrPath path.Path, raw string) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		addError(attrPath, "Invalid Base URL", "`base_url` must be an absolute URL, for example `https://api.example.com`.")
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		addError(attrPath, "Invalid Base URL", fmt.Sprintf("`base_url` scheme must be `http` or `https`, got `%s`.", parsed.Scheme))
	}
}

func addValidationErrors(diags *diag.Diagnostics, validations []codersdk.ValidationError) {
	for _, validation := range validations {
		diags.AddError("Invalid AI Provider Request", fmt.Sprintf("%s: %s", validation.Field, validation.Detail))
	}
}

func bedrockCredentialsConfigured(b *AIProviderBedrockSettingsModel) bool {
	return b != nil &&
		!b.AccessKeyWO.IsNull() && !b.AccessKeyWO.IsUnknown() &&
		!b.AccessKeySecretWO.IsNull() && !b.AccessKeySecretWO.IsUnknown()
}

// credentialsVersionChanged reports whether the planned credential version
// requires resending credentials. A null planned version preserves stored
// credentials rather than rotating them.
func credentialsVersionChanged(plan, state *AIProviderBedrockSettingsModel) bool {
	if plan == nil || plan.CredentialsWOVersion.IsNull() {
		return false
	}
	if state == nil {
		return true
	}
	return !plan.CredentialsWOVersion.Equal(state.CredentialsWOVersion)
}
