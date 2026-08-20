package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// agentsMCPServerMinVersion is the first Coder release that can include the
// organization-scoped MCP server API added on main by coder/coder#27942.
const agentsMCPServerMinVersion = "2.37.0"

var (
	_ resource.Resource                   = &AgentsMCPServerResource{}
	_ resource.ResourceWithConfigure      = &AgentsMCPServerResource{}
	_ resource.ResourceWithImportState    = &AgentsMCPServerResource{}
	_ resource.ResourceWithModifyPlan     = &AgentsMCPServerResource{}
	_ resource.ResourceWithValidateConfig = &AgentsMCPServerResource{}
)

func NewAgentsMCPServerResource() resource.Resource {
	return &AgentsMCPServerResource{}
}

type AgentsMCPServerResource struct {
	data *CoderdProviderData
}

type AgentsMCPServerResourceModel struct {
	ID                          UUID         `tfsdk:"id"`
	OrganizationID              UUID         `tfsdk:"organization_id"`
	DisplayName                 types.String `tfsdk:"display_name"`
	Slug                        types.String `tfsdk:"slug"`
	URL                         types.String `tfsdk:"url"`
	Description                 types.String `tfsdk:"description"`
	IconURL                     types.String `tfsdk:"icon_url"`
	Transport                   types.String `tfsdk:"transport"`
	AuthType                    types.String `tfsdk:"auth_type"`
	Availability                types.String `tfsdk:"availability"`
	OAuth2ClientID              types.String `tfsdk:"oauth2_client_id"`
	OAuth2ClientSecretWO        types.String `tfsdk:"oauth2_client_secret_wo"`
	OAuth2ClientSecretWOVersion types.Int64  `tfsdk:"oauth2_client_secret_wo_version"`
	OAuth2AuthURL               types.String `tfsdk:"oauth2_auth_url"`
	OAuth2TokenURL              types.String `tfsdk:"oauth2_token_url"`
	OAuth2RevocationURL         types.String `tfsdk:"oauth2_revocation_url"`
	OAuth2Scopes                types.String `tfsdk:"oauth2_scopes"`
	APIKeyHeader                types.String `tfsdk:"api_key_header"`
	APIKeyValueWO               types.String `tfsdk:"api_key_value_wo"`
	APIKeyValueWOVersion        types.Int64  `tfsdk:"api_key_value_wo_version"`
	CustomHeadersWO             types.Map    `tfsdk:"custom_headers_wo"`
	CustomHeadersWOVersion      types.Int64  `tfsdk:"custom_headers_wo_version"`
	ToolAllowList               types.Set    `tfsdk:"tool_allow_list"`
	ToolDenyList                types.Set    `tfsdk:"tool_deny_list"`
	Enabled                     types.Bool   `tfsdk:"enabled"`
	ModelIntent                 types.Bool   `tfsdk:"model_intent"`
	AllowInPlanMode             types.Bool   `tfsdk:"allow_in_plan_mode"`
	ForwardCoderHeaders         types.Bool   `tfsdk:"forward_coder_headers"`
	CreatedAt                   types.Int64  `tfsdk:"created_at"`
	UpdatedAt                   types.Int64  `tfsdk:"updated_at"`
}

func (r *AgentsMCPServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents_mcp_server"
}

func (r *AgentsMCPServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_agents_mcp_server is experimental. Changes are expected, and it is not recommended for production use.",
	)
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, config AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || plan.AuthType.IsUnknown() {
		return
	}
	var state AgentsMCPServerResourceModel
	hasState := !req.State.Raw.IsNull()
	if hasState {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	entering := !hasState || !plan.AuthType.Equal(state.AuthType)

	if !entering {
		// A version bump rotates the active auth type's secret in place, so
		// the write-only value must accompany it or the apply is guaranteed
		// to fail.
		switch plan.AuthType.ValueString() {
		case "oauth2":
			if writeOnlyVersionChanged(plan.OAuth2ClientSecretWOVersion, state.OAuth2ClientSecretWOVersion) && config.OAuth2ClientSecretWO.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("oauth2_client_secret_wo"), "Missing OAuth2 Client Secret", "`oauth2_client_secret_wo` must be configured when `oauth2_client_secret_wo_version` changes.")
			}
		case "api_key":
			if writeOnlyVersionChanged(plan.APIKeyValueWOVersion, state.APIKeyValueWOVersion) && config.APIKeyValueWO.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "`api_key_value_wo` must be configured when `api_key_value_wo_version` changes.")
			}
		case "custom_headers":
			if writeOnlyVersionChanged(plan.CustomHeadersWOVersion, state.CustomHeadersWOVersion) && config.CustomHeadersWO.IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "`custom_headers_wo` must be configured when `custom_headers_wo_version` changes.")
			}
		}
		return
	}

	// Creating a server with an auth type, or transitioning an existing one
	// into it, requires that type's credentials because the server rejects or
	// clears the previous ones. Reject such plans now instead of failing at
	// apply time; unknown values are deferred to the apply-time checks.
	switch plan.AuthType.ValueString() {
	case "oauth2":
		if !hasState {
			// Creation with the manual trio omitted uses server-side
			// discovery, and ValidateConfig already rejects partial sets.
			return
		}
		// Discovery only runs at creation, so a transition into oauth2 on an
		// existing server must supply the full manual configuration.
		fields := []struct {
			path  path.Path
			value types.String
		}{
			{path.Root("oauth2_client_id"), config.OAuth2ClientID},
			{path.Root("oauth2_auth_url"), config.OAuth2AuthURL},
			{path.Root("oauth2_token_url"), config.OAuth2TokenURL},
		}
		for _, field := range fields {
			// An unknown value defers only its own check to apply time; the
			// remaining checks are decidable now and must still run.
			if field.value.IsUnknown() {
				continue
			}
			if field.value.IsNull() {
				resp.Diagnostics.AddAttributeError(field.path, "Missing OAuth2 Configuration", "Changing `auth_type` to \"oauth2\" on an existing server requires `oauth2_client_id`, `oauth2_auth_url`, and `oauth2_token_url`, because OAuth2 discovery only runs at creation. To use discovery instead, replace the resource.")
			}
		}
		// The secret itself stays optional for manual configuration, but a
		// version bump during the transition is a rotation and must carry it.
		if writeOnlyVersionChanged(plan.OAuth2ClientSecretWOVersion, state.OAuth2ClientSecretWOVersion) && config.OAuth2ClientSecretWO.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("oauth2_client_secret_wo"), "Missing OAuth2 Client Secret", "`oauth2_client_secret_wo` must be configured when `oauth2_client_secret_wo_version` changes.")
		}
	case "api_key":
		if config.APIKeyHeader.IsNull() || (!config.APIKeyHeader.IsUnknown() && config.APIKeyHeader.ValueString() == "") {
			resp.Diagnostics.AddAttributeError(path.Root("api_key_header"), "Missing API Key Header", "`api_key_header` must be configured when creating a server with `auth_type = \"api_key\"` or changing its auth type to it.")
		}
		if config.APIKeyValueWO.IsNull() {
			resp.Diagnostics.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "`api_key_value_wo` must be configured when creating a server with `auth_type = \"api_key\"` or changing its auth type to it.")
		}
	case "custom_headers":
		if config.CustomHeadersWO.IsNull() || (!config.CustomHeadersWO.IsUnknown() && len(config.CustomHeadersWO.Elements()) == 0) {
			resp.Diagnostics.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "A non-empty `custom_headers_wo` map must be configured when creating a server with `auth_type = \"custom_headers\"` or changing its auth type to it.")
		}
	}
}

func (r *AgentsMCPServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	emptyStringSet := types.SetValueMust(types.StringType, []attr.Value{})
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"~> **Warning**\nThis resource is only compatible with Coder version [" + agentsMCPServerMinVersion + "](https://github.com/coder/coder/releases/tag/v" + agentsMCPServerMinVersion + ") and later.\n\n" +
			"-> `_wo` attributes are [write-only](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments): their values are sent to Coder but never stored in Terraform state. This resource therefore requires Terraform 1.11 or later.\n\n" +
			"Configures an organization-scoped MCP server for Coder Agents. Import IDs use `<organization_id>/<id>`. Changing `url`, `auth_type`, `oauth2_token_url`, `oauth2_revocation_url`, or `oauth2_client_id` invalidates users' stored OAuth tokens.\n\n" +
			"Coder runs OAuth2 discovery and dynamic client registration only when a server is created with `auth_type = \"oauth2\"` and no manual endpoints; updates never re-run discovery. To switch an existing server from manual OAuth2 configuration back to discovery, replace the resource (for example with `terraform apply -replace`). Removing the manual OAuth2 attributes from configuration leaves the stored values unmanaged rather than clearing them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "MCP server configuration ID.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "Organization ID that owns the MCP server configuration. Defaults to the provider default organization ID.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name shown in Coder.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Organization-unique slug for the MCP server. The slug can be updated in place.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "MCP server URL. Changing this value invalidates users' stored OAuth tokens.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description shown in Coder.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"icon_url": schema.StringAttribute{
				MarkdownDescription: "Icon URL shown in Coder.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"transport": schema.StringAttribute{
				MarkdownDescription: "MCP transport. Valid values are `streamable_http` and `sse`. Defaults to `streamable_http`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("streamable_http"),
				Validators: []validator.String{
					stringvalidator.OneOf("streamable_http", "sse"),
				},
			},
			"auth_type": schema.StringAttribute{
				MarkdownDescription: "Authentication type. Valid values are `none`, `oauth2`, `api_key`, `custom_headers`, and `user_oidc`. Defaults to `none`. Changing this value invalidates users' stored OAuth tokens and clears secrets for the previous authentication type.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("none"),
				Validators: []validator.String{
					stringvalidator.OneOf("none", "oauth2", "api_key", "custom_headers", "user_oidc"),
				},
			},
			"availability": schema.StringAttribute{
				MarkdownDescription: "Availability policy. Valid values are `force_on`, `default_on`, and `default_off`. Defaults to `default_off`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("default_off"),
				Validators: []validator.String{
					stringvalidator.OneOf("force_on", "default_on", "default_off"),
				},
			},
			"oauth2_client_id": schema.StringAttribute{
				MarkdownDescription: "OAuth2 client ID. Leave this, `oauth2_auth_url`, and `oauth2_token_url` unset at creation to use OAuth discovery and dynamic client registration; switching an existing server back to discovery requires replacing the resource. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					unknownWhenAuthTypeChanges(),
				},
			},
			"oauth2_client_secret_wo": schema.StringAttribute{
				MarkdownDescription: "OAuth2 client secret. Bump `oauth2_client_secret_wo_version` to rotate it.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.AlsoRequires(path.MatchRoot("oauth2_client_secret_wo_version")),
				},
			},
			"oauth2_client_secret_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version for the write-only OAuth2 client secret. Bump it whenever the secret changes.",
				Optional:            true,
			},
			"oauth2_auth_url": schema.StringAttribute{
				MarkdownDescription: "OAuth2 authorization URL. It can be populated by server-side discovery.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					unknownWhenAuthTypeChanges(),
				},
			},
			"oauth2_token_url": schema.StringAttribute{
				MarkdownDescription: "OAuth2 token URL. It can be populated by server-side discovery. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					unknownWhenAuthTypeChanges(),
				},
			},
			"oauth2_revocation_url": schema.StringAttribute{
				MarkdownDescription: "OAuth2 token revocation URL. It can be populated by server-side discovery. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					unknownWhenAuthTypeChanges(),
				},
			},
			"oauth2_scopes": schema.StringAttribute{
				MarkdownDescription: "Space-separated OAuth2 scopes.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"api_key_header": schema.StringAttribute{
				MarkdownDescription: "HTTP header used for API key authentication. It must be configured when creating a server with `auth_type = \"api_key\"` or changing its auth type to it.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					unknownWhenAuthTypeChanges(),
				},
			},
			"api_key_value_wo": schema.StringAttribute{
				MarkdownDescription: "API key value. Bump `api_key_value_wo_version` to rotate it.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					stringvalidator.AlsoRequires(path.MatchRoot("api_key_value_wo_version")),
				},
			},
			"api_key_value_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version for the write-only API key value. Bump it whenever the value changes.",
				Optional:            true,
			},
			"custom_headers_wo": schema.MapAttribute{
				MarkdownDescription: "HTTP headers used for custom header authentication. Bump `custom_headers_wo_version` to replace them.",
				ElementType:         types.StringType,
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.AlsoRequires(path.MatchRoot("custom_headers_wo_version")),
				},
			},
			"custom_headers_wo_version": schema.Int64Attribute{
				MarkdownDescription: "Version for the write-only custom headers. Bump it whenever the map changes.",
				Optional:            true,
			},
			"tool_allow_list": schema.SetAttribute{
				MarkdownDescription: "Tool names that are allowed. An empty set allows all tools unless denied.",
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(emptyStringSet),
			},
			"tool_deny_list": schema.SetAttribute{
				MarkdownDescription: "Tool names that are denied.",
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(emptyStringSet),
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the MCP server is enabled. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"model_intent": schema.BoolAttribute{
				MarkdownDescription: "Whether the model may select this MCP server based on intent. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"allow_in_plan_mode": schema.BoolAttribute{
				MarkdownDescription: "Whether tools from this server are available in plan mode. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"forward_coder_headers": schema.BoolAttribute{
				MarkdownDescription: "Whether Coder identity headers are forwarded to the MCP server. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"created_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp when the MCP server configuration was created.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp when the MCP server configuration was last updated.",
				Computed:            true,
			},
		},
	}
}

func (r *AgentsMCPServerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AgentsMCPServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.AuthType.IsUnknown() {
		return
	}
	// A null auth_type takes the schema default.
	authType := "none"
	if !data.AuthType.IsNull() {
		authType = data.AuthType.ValueString()
	}

	// The server clears readable auth fields that do not belong to the
	// effective auth type when it changes, so a configured value for another
	// auth type cannot survive an apply and would make the result
	// inconsistent with the plan. Write-only attributes stay configurable
	// because they never enter state and non-destination version bumps are
	// ignored.
	type authField struct {
		name  string
		value types.String
	}
	if authType != "oauth2" {
		for _, field := range []authField{
			{"oauth2_client_id", data.OAuth2ClientID},
			{"oauth2_auth_url", data.OAuth2AuthURL},
			{"oauth2_token_url", data.OAuth2TokenURL},
			{"oauth2_revocation_url", data.OAuth2RevocationURL},
			{"oauth2_scopes", data.OAuth2Scopes},
		} {
			if !field.value.IsNull() && !field.value.IsUnknown() {
				resp.Diagnostics.AddAttributeError(path.Root(field.name), "Invalid Attribute Combination", fmt.Sprintf("`%s` can only be configured when `auth_type` is \"oauth2\".", field.name))
			}
		}
	}
	if authType != "api_key" && !data.APIKeyHeader.IsNull() && !data.APIKeyHeader.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_key_header"), "Invalid Attribute Combination", "`api_key_header` can only be configured when `auth_type` is \"api_key\".")
	}

	// Write-only secrets are only required at creation or on an auth type
	// transition, so api_key and custom_headers completeness is checked in
	// Create and updateRequest instead. Requiring them here would reject
	// configs that adopt imported servers whose secrets cannot be read back.
	switch authType {
	case "oauth2":
		fields := []struct {
			path  path.Path
			value types.String
		}{
			{path.Root("oauth2_client_id"), data.OAuth2ClientID},
			{path.Root("oauth2_auth_url"), data.OAuth2AuthURL},
			{path.Root("oauth2_token_url"), data.OAuth2TokenURL},
		}
		configured := 0
		unset := 0
		for _, field := range fields {
			// An unknown field is skipped rather than deferring the whole
			// check: mixing a configured field with an unset one is
			// incomplete no matter what the unknown resolves to.
			if field.value.IsUnknown() {
				continue
			}
			if !field.value.IsNull() && field.value.ValueString() != "" {
				configured++
			} else {
				unset++
			}
		}
		if configured != 0 && unset != 0 {
			for _, field := range fields {
				resp.Diagnostics.AddAttributeError(field.path, "Incomplete OAuth2 Configuration", "Set `oauth2_client_id`, `oauth2_auth_url`, and `oauth2_token_url` together for manual OAuth2 configuration, or leave all three unset to use discovery and dynamic client registration.")
			}
		}
	}
}

func (r *AgentsMCPServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.OrganizationID.IsNull() || plan.OrganizationID.IsUnknown() {
		plan.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	createReq := plan.createRequest(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "creating MCP server", map[string]any{"organization_id": plan.OrganizationID.ValueString()})
	server, err := r.data.Client.CreateMCPServerConfig(ctx, plan.OrganizationID.ValueUUID(), createReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create MCP server, got error: %s", err))
		return
	}
	state := plan.stateFromServer(server)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AgentsMCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	server, err := r.data.Client.MCPServerConfigByID(ctx, state.OrganizationID.ValueUUID(), state.ID.ValueUUID())
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("MCP server %s not found. Marking as deleted.", state.ID.ValueString()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP server, got error: %s", err))
		return
	}
	refreshed := state.stateFromServer(server)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *AgentsMCPServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updateReq := plan.updateRequest(ctx, state, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "updating MCP server", map[string]any{"id": state.ID.ValueString()})
	server, err := r.data.Client.UpdateMCPServerConfig(ctx, state.OrganizationID.ValueUUID(), state.ID.ValueUUID(), updateReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update MCP server, got error: %s", err))
		return
	}
	updated := plan.stateFromServer(server)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *AgentsMCPServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AgentsMCPServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "deleting MCP server", map[string]any{"id": state.ID.ValueString()})
	if err := r.data.Client.DeleteMCPServerConfig(ctx, state.OrganizationID.ValueUUID(), state.ID.ValueUUID()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP server, got error: %s", err))
	}
}

func (r *AgentsMCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
		resp.Diagnostics.AddError("Invalid Import ID", fmt.Sprintf("Unable to parse MCP server ID as UUID: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), organizationID.String())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id.String())...)
}

func (m AgentsMCPServerResourceModel) createRequest(ctx context.Context, config AgentsMCPServerResourceModel, diags *diag.Diagnostics) codersdk.CreateMCPServerConfigRequest {
	switch m.AuthType.ValueString() {
	case "api_key":
		if m.APIKeyHeader.ValueString() == "" {
			diags.AddAttributeError(path.Root("api_key_header"), "Missing API Key Header", "`auth_type = \"api_key\"` requires a non-empty `api_key_header`.")
		}
		if writeOnlyString(config.APIKeyValueWO) == "" {
			diags.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "Creating a server with `auth_type = \"api_key\"` requires `api_key_value_wo`.")
		}
	case "custom_headers":
		if len(writeOnlyStringMap(ctx, config.CustomHeadersWO, diags)) == 0 {
			diags.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "Creating a server with `auth_type = \"custom_headers\"` requires a non-empty `custom_headers_wo` map.")
		}
	}
	req := codersdk.CreateMCPServerConfigRequest{
		DisplayName:         m.DisplayName.ValueString(),
		Slug:                m.Slug.ValueString(),
		Description:         m.Description.ValueString(),
		IconURL:             m.IconURL.ValueString(),
		Transport:           m.Transport.ValueString(),
		URL:                 m.URL.ValueString(),
		AuthType:            m.AuthType.ValueString(),
		OAuth2ClientID:      m.OAuth2ClientID.ValueString(),
		OAuth2AuthURL:       m.OAuth2AuthURL.ValueString(),
		OAuth2TokenURL:      m.OAuth2TokenURL.ValueString(),
		OAuth2RevocationURL: m.OAuth2RevocationURL.ValueString(),
		OAuth2Scopes:        m.OAuth2Scopes.ValueString(),
		APIKeyHeader:        m.APIKeyHeader.ValueString(),
		ToolAllowList:       stringSetElements(ctx, m.ToolAllowList, diags),
		ToolDenyList:        stringSetElements(ctx, m.ToolDenyList, diags),
		Availability:        m.Availability.ValueString(),
		Enabled:             m.Enabled.ValueBool(),
		ModelIntent:         m.ModelIntent.ValueBool(),
		AllowInPlanMode:     m.AllowInPlanMode.ValueBool(),
		ForwardCoderHeaders: m.ForwardCoderHeaders.ValueBool(),
	}
	// A write-only secret is sent only when its auth type is selected. The
	// server persists incoming secrets regardless of auth type, so a
	// leftover credential for another type must not be transmitted.
	switch m.AuthType.ValueString() {
	case "oauth2":
		req.OAuth2ClientSecret = writeOnlyString(config.OAuth2ClientSecretWO)
	case "api_key":
		req.APIKeyValue = writeOnlyString(config.APIKeyValueWO)
	case "custom_headers":
		req.CustomHeaders = writeOnlyStringMap(ctx, config.CustomHeadersWO, diags)
	}
	return req
}

func (m AgentsMCPServerResourceModel) updateRequest(ctx context.Context, state, config AgentsMCPServerResourceModel, diags *diag.Diagnostics) codersdk.UpdateMCPServerConfigRequest {
	// The PATCH is sparse: omitted fields keep their server-side value, so
	// only fields whose planned value differs from state are sent. Unknown
	// planned values become nil pointers and are omitted, leaving them to
	// the server (for example when an auth type change resets a field).
	var req codersdk.UpdateMCPServerConfigRequest
	if !m.DisplayName.Equal(state.DisplayName) {
		req.DisplayName = stringPtrOrNil(m.DisplayName)
	}
	if !m.Slug.Equal(state.Slug) {
		req.Slug = stringPtrOrNil(m.Slug)
	}
	if !m.Description.Equal(state.Description) {
		req.Description = stringPtrOrNil(m.Description)
	}
	if !m.IconURL.Equal(state.IconURL) {
		req.IconURL = stringPtrOrNil(m.IconURL)
	}
	if !m.Transport.Equal(state.Transport) {
		req.Transport = stringPtrOrNil(m.Transport)
	}
	if !m.URL.Equal(state.URL) {
		req.URL = stringPtrOrNil(m.URL)
	}
	if !m.AuthType.Equal(state.AuthType) {
		req.AuthType = stringPtrOrNil(m.AuthType)
	}
	if !m.OAuth2ClientID.Equal(state.OAuth2ClientID) {
		req.OAuth2ClientID = stringPtrOrNil(m.OAuth2ClientID)
	}
	if !m.OAuth2AuthURL.Equal(state.OAuth2AuthURL) {
		req.OAuth2AuthURL = stringPtrOrNil(m.OAuth2AuthURL)
	}
	if !m.OAuth2TokenURL.Equal(state.OAuth2TokenURL) {
		req.OAuth2TokenURL = stringPtrOrNil(m.OAuth2TokenURL)
	}
	if !m.OAuth2RevocationURL.Equal(state.OAuth2RevocationURL) {
		req.OAuth2RevocationURL = stringPtrOrNil(m.OAuth2RevocationURL)
	}
	if !m.OAuth2Scopes.Equal(state.OAuth2Scopes) {
		req.OAuth2Scopes = stringPtrOrNil(m.OAuth2Scopes)
	}
	if !m.APIKeyHeader.Equal(state.APIKeyHeader) {
		req.APIKeyHeader = stringPtrOrNil(m.APIKeyHeader)
	}
	if !m.ToolAllowList.IsUnknown() && !m.ToolAllowList.Equal(state.ToolAllowList) {
		toolAllowList := stringSetElements(ctx, m.ToolAllowList, diags)
		req.ToolAllowList = &toolAllowList
	}
	if !m.ToolDenyList.IsUnknown() && !m.ToolDenyList.Equal(state.ToolDenyList) {
		toolDenyList := stringSetElements(ctx, m.ToolDenyList, diags)
		req.ToolDenyList = &toolDenyList
	}
	if !m.Availability.Equal(state.Availability) {
		req.Availability = stringPtrOrNil(m.Availability)
	}
	if !m.Enabled.Equal(state.Enabled) {
		req.Enabled = m.Enabled.ValueBoolPointer()
	}
	if !m.ModelIntent.Equal(state.ModelIntent) {
		req.ModelIntent = m.ModelIntent.ValueBoolPointer()
	}
	if !m.AllowInPlanMode.Equal(state.AllowInPlanMode) {
		req.AllowInPlanMode = m.AllowInPlanMode.ValueBoolPointer()
	}
	if !m.ForwardCoderHeaders.Equal(state.ForwardCoderHeaders) {
		req.ForwardCoderHeaders = m.ForwardCoderHeaders.ValueBoolPointer()
	}
	// A secret is sent only when its auth type is the destination: on a
	// version bump for rotation, or on a transition into the type because the
	// server clears the previous type's secrets. Version bumps for other auth
	// types are ignored rather than resending a stale secret.
	newAuthType := m.AuthType.ValueString()
	authTypeChanged := !m.AuthType.Equal(state.AuthType)

	if newAuthType == "oauth2" {
		oauth2Secret := writeOnlyString(config.OAuth2ClientSecretWO)
		if writeOnlyVersionChanged(m.OAuth2ClientSecretWOVersion, state.OAuth2ClientSecretWOVersion) {
			if oauth2Secret == "" {
				diags.AddAttributeError(path.Root("oauth2_client_secret_wo"), "Missing OAuth2 Client Secret", "`oauth2_client_secret_wo` must be configured when `oauth2_client_secret_wo_version` changes.")
			} else {
				req.OAuth2ClientSecret = &oauth2Secret
			}
		} else if authTypeChanged && oauth2Secret != "" {
			req.OAuth2ClientSecret = &oauth2Secret
		}
	}

	if newAuthType == "api_key" {
		if authTypeChanged && m.APIKeyHeader.ValueString() == "" {
			diags.AddAttributeError(path.Root("api_key_header"), "Missing API Key Header", "`api_key_header` must be configured when `auth_type` changes to \"api_key\".")
		}
		apiKeyValue := writeOnlyString(config.APIKeyValueWO)
		if writeOnlyVersionChanged(m.APIKeyValueWOVersion, state.APIKeyValueWOVersion) || authTypeChanged {
			if apiKeyValue == "" {
				diags.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "`api_key_value_wo` must be configured when `api_key_value_wo_version` changes or `auth_type` changes to \"api_key\".")
			} else {
				req.APIKeyValue = &apiKeyValue
			}
		}
	}

	if newAuthType == "custom_headers" {
		customHeaders := writeOnlyStringMap(ctx, config.CustomHeadersWO, diags)
		if writeOnlyVersionChanged(m.CustomHeadersWOVersion, state.CustomHeadersWOVersion) || authTypeChanged {
			if len(customHeaders) == 0 {
				diags.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "`custom_headers_wo` must be configured when `custom_headers_wo_version` changes or `auth_type` changes to \"custom_headers\".")
			} else {
				req.CustomHeaders = &customHeaders
			}
		}
	}
	return req
}

func (m AgentsMCPServerResourceModel) stateFromServer(server codersdk.MCPServerConfig) AgentsMCPServerResourceModel {
	return AgentsMCPServerResourceModel{
		ID:             UUIDValue(server.ID),
		OrganizationID: UUIDValue(server.OrganizationID),
		DisplayName:    types.StringValue(server.DisplayName),
		Slug:           types.StringValue(server.Slug),
		URL:            types.StringValue(server.URL),
		Description:    types.StringValue(server.Description),
		IconURL:        types.StringValue(server.IconURL),
		Transport:      types.StringValue(server.Transport),
		AuthType:       types.StringValue(server.AuthType),
		// Optional strings without static defaults map an omitted server
		// value ("") to null, matching how Terraform represents unset.
		Availability:                types.StringValue(server.Availability),
		OAuth2ClientID:              stringValueOrNull(server.OAuth2ClientID),
		OAuth2ClientSecretWO:        types.StringNull(),
		OAuth2ClientSecretWOVersion: m.OAuth2ClientSecretWOVersion,
		OAuth2AuthURL:               stringValueOrNull(server.OAuth2AuthURL),
		OAuth2TokenURL:              stringValueOrNull(server.OAuth2TokenURL),
		OAuth2RevocationURL:         stringValueOrNull(server.OAuth2RevocationURL),
		OAuth2Scopes:                types.StringValue(server.OAuth2Scopes),
		APIKeyHeader:                stringValueOrNull(server.APIKeyHeader),
		APIKeyValueWO:               types.StringNull(),
		APIKeyValueWOVersion:        m.APIKeyValueWOVersion,
		CustomHeadersWO:             types.MapNull(types.StringType),
		CustomHeadersWOVersion:      m.CustomHeadersWOVersion,
		ToolAllowList:               stringSetValue(server.ToolAllowList),
		ToolDenyList:                stringSetValue(server.ToolDenyList),
		Enabled:                     types.BoolValue(server.Enabled),
		ModelIntent:                 types.BoolValue(server.ModelIntent),
		AllowInPlanMode:             types.BoolValue(server.AllowInPlanMode),
		ForwardCoderHeaders:         types.BoolValue(server.ForwardCoderHeaders),
		CreatedAt:                   types.Int64Value(server.CreatedAt.Unix()),
		UpdatedAt:                   types.Int64Value(server.UpdatedAt.Unix()),
	}
}

func writeOnlyString(value types.String) string {
	if value.IsNull() || value.IsUnknown() {
		return ""
	}
	return value.ValueString()
}

func writeOnlyStringMap(ctx context.Context, value types.Map, diags *diag.Diagnostics) map[string]string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var result map[string]string
	diags.Append(value.ElementsAs(ctx, &result, false)...)
	return result
}

func stringSetElements(ctx context.Context, value types.Set, diags *diag.Diagnostics) []string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	var result []string
	diags.Append(value.ElementsAs(ctx, &result, false)...)
	return result
}

func stringSetValue(values []string) types.Set {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.SetValueMust(types.StringType, elements)
}

func writeOnlyVersionChanged(plan, state types.Int64) bool {
	return !plan.IsNull() && !plan.IsUnknown() && !plan.Equal(state)
}
