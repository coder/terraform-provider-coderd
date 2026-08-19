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

var (
	_ resource.Resource                   = &MCPServerResource{}
	_ resource.ResourceWithConfigure      = &MCPServerResource{}
	_ resource.ResourceWithImportState    = &MCPServerResource{}
	_ resource.ResourceWithModifyPlan     = &MCPServerResource{}
	_ resource.ResourceWithValidateConfig = &MCPServerResource{}
)

func NewMCPServerResource() resource.Resource {
	return &MCPServerResource{}
}

type MCPServerResource struct {
	data *CoderdProviderData
}

type MCPServerResourceModel struct {
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

func (r *MCPServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *MCPServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_mcp_server is experimental. Changes are expected, and it is not recommended for production use.",
	)
}

func (r *MCPServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	emptyStringSet := types.SetValueMust(types.StringType, []attr.Value{})
	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"-> `_wo` attributes are [write-only](https://developer.hashicorp.com/terraform/language/resources/ephemeral#write-only-arguments): their values are sent to Coder but never stored in Terraform state. This resource therefore requires Terraform 1.11 or later.\n\n" +
			"Configures an organization-scoped MCP server for Coder Agents. Import IDs use `<organization_id>/<id>`. Changing `url`, `auth_type`, `oauth2_token_url`, `oauth2_revocation_url`, or `oauth2_client_id` invalidates users' stored OAuth tokens.",
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
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Organization-unique slug for the MCP server. The slug can be updated in place.",
				Required:            true,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "MCP server URL. Changing this value invalidates users' stored OAuth tokens.",
				Required:            true,
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
				MarkdownDescription: "MCP transport. Valid values are `streamable_http` and `sse`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("streamable_http"),
				Validators: []validator.String{
					stringvalidator.OneOf("streamable_http", "sse"),
				},
			},
			"auth_type": schema.StringAttribute{
				MarkdownDescription: "Authentication type. Valid values are `none`, `oauth2`, `api_key`, `custom_headers`, and `user_oidc`. Changing this value invalidates users' stored OAuth tokens and clears secrets for the previous authentication type.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("none"),
				Validators: []validator.String{
					stringvalidator.OneOf("none", "oauth2", "api_key", "custom_headers", "user_oidc"),
				},
			},
			"availability": schema.StringAttribute{
				MarkdownDescription: "Availability policy. Valid values are `force_on`, `default_on`, and `default_off`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("default_off"),
				Validators: []validator.String{
					stringvalidator.OneOf("force_on", "default_on", "default_off"),
				},
			},
			"oauth2_client_id": schema.StringAttribute{
				MarkdownDescription: "OAuth2 client ID. Leave this, `oauth2_auth_url`, and `oauth2_token_url` unset to use OAuth discovery and dynamic client registration. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
			},
			"oauth2_client_secret_wo": schema.StringAttribute{
				MarkdownDescription: "OAuth2 client secret. Bump `oauth2_client_secret_wo_version` to rotate it.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
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
			},
			"oauth2_token_url": schema.StringAttribute{
				MarkdownDescription: "OAuth2 token URL. It can be populated by server-side discovery. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
			},
			"oauth2_revocation_url": schema.StringAttribute{
				MarkdownDescription: "OAuth2 token revocation URL. It can be populated by server-side discovery. Changing it invalidates users' stored OAuth tokens.",
				Optional:            true,
				Computed:            true,
			},
			"oauth2_scopes": schema.StringAttribute{
				MarkdownDescription: "Space-separated OAuth2 scopes.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"api_key_header": schema.StringAttribute{
				MarkdownDescription: "HTTP header used for API key authentication. The server may populate a default.",
				Optional:            true,
				Computed:            true,
			},
			"api_key_value_wo": schema.StringAttribute{
				MarkdownDescription: "API key value. Bump `api_key_value_wo_version` to rotate it.",
				Optional:            true,
				Sensitive:           true,
				WriteOnly:           true,
				Validators: []validator.String{
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
				MarkdownDescription: "Whether the MCP server is enabled.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"model_intent": schema.BoolAttribute{
				MarkdownDescription: "Whether the model may select this MCP server based on intent.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"allow_in_plan_mode": schema.BoolAttribute{
				MarkdownDescription: "Whether tools from this server are available in plan mode.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"forward_coder_headers": schema.BoolAttribute{
				MarkdownDescription: "Whether Coder identity headers are forwarded to the MCP server.",
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

func (r *MCPServerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *MCPServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data MCPServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.AuthType.IsNull() || data.AuthType.IsUnknown() {
		return
	}

	switch data.AuthType.ValueString() {
	case "api_key":
		if isEmptyString(data.APIKeyHeader) {
			resp.Diagnostics.AddAttributeError(path.Root("api_key_header"), "Missing API Key Header", "`auth_type = \"api_key\"` requires a non-empty `api_key_header`.")
		}
		if isEmptyString(data.APIKeyValueWO) {
			resp.Diagnostics.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "`auth_type = \"api_key\"` requires a non-empty `api_key_value_wo`.")
		}
	case "custom_headers":
		if data.CustomHeadersWO.IsUnknown() {
			return
		}
		if data.CustomHeadersWO.IsNull() || len(data.CustomHeadersWO.Elements()) == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "`auth_type = \"custom_headers\"` requires a non-empty `custom_headers_wo` map.")
		}
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
		for _, field := range fields {
			if field.value.IsUnknown() {
				return
			}
			if !field.value.IsNull() && field.value.ValueString() != "" {
				configured++
			}
		}
		if configured != 0 && configured != len(fields) {
			for _, field := range fields {
				resp.Diagnostics.AddAttributeError(field.path, "Incomplete OAuth2 Configuration", "Set `oauth2_client_id`, `oauth2_auth_url`, and `oauth2_token_url` together for manual OAuth2 configuration, or leave all three unset to use discovery and dynamic client registration.")
			}
		}
	}
}

func (r *MCPServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config MCPServerResourceModel
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
	state := plan.stateFromServer(ctx, server, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *MCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state MCPServerResourceModel
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
	refreshed := state.stateFromServer(ctx, server, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &refreshed)...)
}

func (r *MCPServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config MCPServerResourceModel
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
	updated := plan.stateFromServer(ctx, server, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &updated)...)
}

func (r *MCPServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state MCPServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tflog.Info(ctx, "deleting MCP server", map[string]any{"id": state.ID.ValueString()})
	if err := r.data.Client.DeleteMCPServerConfig(ctx, state.OrganizationID.ValueUUID(), state.ID.ValueUUID()); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP server, got error: %s", err))
	}
}

func (r *MCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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

func (m MCPServerResourceModel) createRequest(ctx context.Context, config MCPServerResourceModel, diags *diag.Diagnostics) codersdk.CreateMCPServerConfigRequest {
	return codersdk.CreateMCPServerConfigRequest{
		DisplayName:         m.DisplayName.ValueString(),
		Slug:                m.Slug.ValueString(),
		Description:         m.Description.ValueString(),
		IconURL:             m.IconURL.ValueString(),
		Transport:           m.Transport.ValueString(),
		URL:                 m.URL.ValueString(),
		AuthType:            m.AuthType.ValueString(),
		OAuth2ClientID:      m.OAuth2ClientID.ValueString(),
		OAuth2ClientSecret:  writeOnlyString(config.OAuth2ClientSecretWO),
		OAuth2AuthURL:       m.OAuth2AuthURL.ValueString(),
		OAuth2TokenURL:      m.OAuth2TokenURL.ValueString(),
		OAuth2RevocationURL: m.OAuth2RevocationURL.ValueString(),
		OAuth2Scopes:        m.OAuth2Scopes.ValueString(),
		APIKeyHeader:        m.APIKeyHeader.ValueString(),
		APIKeyValue:         writeOnlyString(config.APIKeyValueWO),
		CustomHeaders:       writeOnlyStringMap(ctx, config.CustomHeadersWO, diags),
		ToolAllowList:       stringSetElements(ctx, m.ToolAllowList, diags),
		ToolDenyList:        stringSetElements(ctx, m.ToolDenyList, diags),
		Availability:        m.Availability.ValueString(),
		Enabled:             m.Enabled.ValueBool(),
		ModelIntent:         m.ModelIntent.ValueBool(),
		AllowInPlanMode:     m.AllowInPlanMode.ValueBool(),
		ForwardCoderHeaders: m.ForwardCoderHeaders.ValueBool(),
	}
}

func (m MCPServerResourceModel) updateRequest(ctx context.Context, state, config MCPServerResourceModel, diags *diag.Diagnostics) codersdk.UpdateMCPServerConfigRequest {
	toolAllowList := stringSetElements(ctx, m.ToolAllowList, diags)
	toolDenyList := stringSetElements(ctx, m.ToolDenyList, diags)
	req := codersdk.UpdateMCPServerConfigRequest{
		DisplayName:         stringPointer(m.DisplayName.ValueString()),
		Slug:                stringPointer(m.Slug.ValueString()),
		Description:         stringPointer(m.Description.ValueString()),
		IconURL:             stringPointer(m.IconURL.ValueString()),
		Transport:           stringPointer(m.Transport.ValueString()),
		URL:                 stringPointer(m.URL.ValueString()),
		AuthType:            stringPointer(m.AuthType.ValueString()),
		OAuth2ClientID:      stringPointer(m.OAuth2ClientID.ValueString()),
		OAuth2AuthURL:       nonEmptyStringPointer(m.OAuth2AuthURL.ValueString()),
		OAuth2TokenURL:      nonEmptyStringPointer(m.OAuth2TokenURL.ValueString()),
		OAuth2RevocationURL: stringPointer(m.OAuth2RevocationURL.ValueString()),
		OAuth2Scopes:        stringPointer(m.OAuth2Scopes.ValueString()),
		APIKeyHeader:        stringPointer(m.APIKeyHeader.ValueString()),
		ToolAllowList:       &toolAllowList,
		ToolDenyList:        &toolDenyList,
		Availability:        stringPointer(m.Availability.ValueString()),
		Enabled:             boolPointer(m.Enabled.ValueBool()),
		ModelIntent:         boolPointer(m.ModelIntent.ValueBool()),
		AllowInPlanMode:     boolPointer(m.AllowInPlanMode.ValueBool()),
		ForwardCoderHeaders: boolPointer(m.ForwardCoderHeaders.ValueBool()),
	}
	if writeOnlyVersionChanged(m.OAuth2ClientSecretWOVersion, state.OAuth2ClientSecretWOVersion) {
		secret := writeOnlyString(config.OAuth2ClientSecretWO)
		if secret == "" {
			diags.AddAttributeError(path.Root("oauth2_client_secret_wo"), "Missing OAuth2 Client Secret", "`oauth2_client_secret_wo` must be configured when `oauth2_client_secret_wo_version` changes.")
		} else {
			req.OAuth2ClientSecret = &secret
		}
	}
	if writeOnlyVersionChanged(m.APIKeyValueWOVersion, state.APIKeyValueWOVersion) {
		secret := writeOnlyString(config.APIKeyValueWO)
		if secret == "" {
			diags.AddAttributeError(path.Root("api_key_value_wo"), "Missing API Key Value", "`api_key_value_wo` must be configured when `api_key_value_wo_version` changes.")
		} else {
			req.APIKeyValue = &secret
		}
	}
	if writeOnlyVersionChanged(m.CustomHeadersWOVersion, state.CustomHeadersWOVersion) {
		headers := writeOnlyStringMap(ctx, config.CustomHeadersWO, diags)
		if len(headers) == 0 {
			diags.AddAttributeError(path.Root("custom_headers_wo"), "Missing Custom Headers", "`custom_headers_wo` must be configured when `custom_headers_wo_version` changes.")
		} else {
			req.CustomHeaders = &headers
		}
	}
	return req
}

func (m MCPServerResourceModel) stateFromServer(ctx context.Context, server codersdk.MCPServerConfig, diags *diag.Diagnostics) MCPServerResourceModel {
	return MCPServerResourceModel{
		ID:                          UUIDValue(server.ID),
		OrganizationID:              UUIDValue(server.OrganizationID),
		DisplayName:                 types.StringValue(server.DisplayName),
		Slug:                        types.StringValue(server.Slug),
		URL:                         types.StringValue(server.URL),
		Description:                 types.StringValue(server.Description),
		IconURL:                     types.StringValue(server.IconURL),
		Transport:                   types.StringValue(server.Transport),
		AuthType:                    types.StringValue(server.AuthType),
		Availability:                types.StringValue(server.Availability),
		OAuth2ClientID:              types.StringValue(server.OAuth2ClientID),
		OAuth2ClientSecretWO:        types.StringNull(),
		OAuth2ClientSecretWOVersion: m.OAuth2ClientSecretWOVersion,
		OAuth2AuthURL:               types.StringValue(server.OAuth2AuthURL),
		OAuth2TokenURL:              types.StringValue(server.OAuth2TokenURL),
		OAuth2RevocationURL:         types.StringValue(server.OAuth2RevocationURL),
		OAuth2Scopes:                types.StringValue(server.OAuth2Scopes),
		APIKeyHeader:                types.StringValue(server.APIKeyHeader),
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

func isEmptyString(value types.String) bool {
	return !value.IsUnknown() && (value.IsNull() || value.ValueString() == "")
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

func stringPointer(value string) *string {
	return &value
}

func nonEmptyStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}
