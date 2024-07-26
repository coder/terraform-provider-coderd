package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &TemplateDataSource{}
var _ datasource.DataSourceWithConfigValidators = &TemplateDataSource{}

func NewTemplateDataSource() datasource.DataSource {
	return &TemplateDataSource{}
}

// TemplateDataSource defines the data source implementation.
type TemplateDataSource struct {
	data *CoderdProviderData
}

// TemplateDataSourceModel describes the data source data model.
type TemplateDataSourceModel struct {
	// ((Organization and Name) or ID) must be set
	OrganizationID UUID         `tfsdk:"organization_id"`
	ID             UUID         `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`

	DisplayName types.String `tfsdk:"display_name"`
	// TODO: Provisioner
	Description        types.String `tfsdk:"description"`
	ActiveVersionID    UUID         `tfsdk:"active_version_id"`
	ActiveUserCount    types.Int64  `tfsdk:"active_user_count"`
	Deprecated         types.Bool   `tfsdk:"deprecated"`
	DeprecationMessage types.String `tfsdk:"deprecation_message"`
	Icon               types.String `tfsdk:"icon"`

	DefaultTTLMillis   types.Int64 `tfsdk:"default_ttl_ms"`
	ActivityBumpMillis types.Int64 `tfsdk:"activity_bump_ms"`
	// TODO: AutostopRequirement
	// TODO: AutostartRequirement

	AllowUserAutostart           types.Bool `tfsdk:"allow_user_autostart"`
	AllowUserAutostop            types.Bool `tfsdk:"allow_user_autostop"`
	AllowUserCancelWorkspaceJobs types.Bool `tfsdk:"allow_user_cancel_workspace_jobs"`

	FailureTTLMillis               types.Int64 `tfsdk:"failure_ttl_ms"`
	TimeTilDormantMillis           types.Int64 `tfsdk:"time_til_dormant_ms"`
	TimeTilDormantAutoDeleteMillis types.Int64 `tfsdk:"time_til_dormant_autodelete_ms"`

	RequireActiveVersion types.Bool `tfsdk:"require_active_version"`
	// TODO: MaxPortShareLevel

	CreatedByUserID UUID        `tfsdk:"created_by_user_id"`
	CreatedAt       types.Int64 `tfsdk:"created_at"` // Unix timestamp
	UpdatedAt       types.Int64 `tfsdk:"updated_at"` // Unix timestamp

	// TODO: ACL-related stuff
}

func (d *TemplateDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_template"
}

func (d *TemplateDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing template on the coder deployment",

		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "ID of the organization the template is associated with.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the template to retrieve. This field will be populated if a template name is supplied.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.AtLeastOneOf(path.Expressions{
						path.MatchRoot("name"),
					}...),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the template to retrieve. This field will be populated if an ID is supplied.",
				Optional:            true,
				Computed:            true,
			},

			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the template.",
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the template.",
				Computed:            true,
			},
			"active_version_id": schema.StringAttribute{
				MarkdownDescription: "ID of the active version of the template.",
				CustomType:          UUIDType,
				Computed:            true,
			},
			"active_user_count": schema.Int64Attribute{
				MarkdownDescription: "Number of active users using the template.",
				Computed:            true,
			},
			"deprecated": schema.BoolAttribute{
				MarkdownDescription: "Whether the template is deprecated.",
				Computed:            true,
			},
			"deprecation_message": schema.StringAttribute{
				MarkdownDescription: "Message to display when the template is deprecated.",
				Computed:            true,
			},
			"icon": schema.StringAttribute{
				MarkdownDescription: "URL of the template's icon.",
				Computed:            true,
			},
			"default_ttl_ms": schema.Int64Attribute{
				MarkdownDescription: "Default time-to-live for workspaces created from the template.",
				Computed:            true,
			},
			"activity_bump_ms": schema.Int64Attribute{
				MarkdownDescription: "Duration to bump the deadline of a workspace when it receives activity.",
				Computed:            true,
			},
			"allow_user_autostart": schema.BoolAttribute{
				MarkdownDescription: "Whether users can autostart workspaces created from the template.",
				Computed:            true,
			},
			"allow_user_autostop": schema.BoolAttribute{
				MarkdownDescription: "Whether users can customize autostop behavior for workspaces created from the template.",
				Computed:            true,
			},
			"allow_user_cancel_workspace_jobs": schema.BoolAttribute{
				MarkdownDescription: "Whether users can cancel jobs in workspaces created from the template.",
				Computed:            true,
			},
			"failure_ttl_ms": schema.Int64Attribute{
				MarkdownDescription: "Automatic cleanup TTL for failed workspace builds.",
				Computed:            true,
			},
			"time_til_dormant_ms": schema.Int64Attribute{
				MarkdownDescription: "Duration of inactivity before a workspace is considered dormant.",
				Computed:            true,
			},
			"time_til_dormant_autodelete_ms": schema.Int64Attribute{
				MarkdownDescription: "Duration of inactivity after the workspace becomes dormant before a workspace is automatically deleted.",
				Computed:            true,
			},
			"require_active_version": schema.BoolAttribute{
				MarkdownDescription: "Whether workspaces created from the template must be up-to-datae on the latest active version.",
				Computed:            true,
			},
			"created_by_user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user who created the template.",
				CustomType:          UUIDType,
				Computed:            true,
			},
			"created_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp of when the template was created.",
				Computed:            true,
			},
			"updated_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp of when the template was last updated.",
				Computed:            true,
			},
		},
	}
}

func (d *TemplateDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*CoderdProviderData)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.data = data
}

func (d *TemplateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TemplateDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := d.data.Client

	var (
		template codersdk.Template
		err      error
	)
	if data.ID.ValueUUID() != uuid.Nil {
		template, err = client.Template(ctx, data.ID.ValueUUID())
	} else {
		if data.OrganizationID.ValueUUID() == uuid.Nil {
			data.OrganizationID = UUIDValue(d.data.DefaultOrganizationID)
		}
		if data.OrganizationID.ValueUUID() == uuid.Nil {
			resp.Diagnostics.AddError("Client Error", "name requires organization_id to be set")
			return
		}
		template, err = client.TemplateByName(ctx, data.OrganizationID.ValueUUID(), data.Name.ValueString())
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get template, got error: %s", err))
		return
	}
	if !data.ID.IsNull() && template.ID.String() != data.ID.ValueString() {
		resp.Diagnostics.AddError("Client Error", "Retrieved Template's ID does not match the provided ID")
		return
	}
	if !data.Name.IsNull() && template.Name != data.Name.ValueString() {
		resp.Diagnostics.AddError("Client Error", "Retrieved Template's name does not match the provided name")
		return
	}
	if !data.OrganizationID.IsNull() && template.OrganizationID.String() != data.OrganizationID.ValueString() {
		resp.Diagnostics.AddError("Client Error", "Retrieved Template's organization ID does not match the provided organization ID")
		return
	}

	data.OrganizationID = UUIDValue(template.OrganizationID)
	data.ID = UUIDValue(template.ID)
	data.Name = types.StringValue(template.Name)
	data.DisplayName = types.StringValue(template.DisplayName)
	data.Description = types.StringValue(template.Description)
	data.ActiveVersionID = UUIDValue(template.ActiveVersionID)
	data.ActiveUserCount = types.Int64Value(int64(template.ActiveUserCount))
	data.Deprecated = types.BoolValue(template.Deprecated)
	data.DeprecationMessage = types.StringValue(template.DeprecationMessage)
	data.Icon = types.StringValue(template.Icon)
	data.DefaultTTLMillis = types.Int64Value(template.DefaultTTLMillis)
	data.ActivityBumpMillis = types.Int64Value(template.ActivityBumpMillis)
	data.AllowUserAutostart = types.BoolValue(template.AllowUserAutostart)
	data.AllowUserAutostop = types.BoolValue(template.AllowUserAutostop)
	data.AllowUserCancelWorkspaceJobs = types.BoolValue(template.AllowUserCancelWorkspaceJobs)
	data.FailureTTLMillis = types.Int64Value(template.FailureTTLMillis)
	data.TimeTilDormantMillis = types.Int64Value(template.TimeTilDormantMillis)
	data.TimeTilDormantAutoDeleteMillis = types.Int64Value(template.TimeTilDormantAutoDeleteMillis)
	data.RequireActiveVersion = types.BoolValue(template.RequireActiveVersion)
	data.CreatedByUserID = UUIDValue(template.CreatedByID)
	data.CreatedAt = types.Int64Value(template.CreatedAt.Unix())
	data.UpdatedAt = types.Int64Value(template.UpdatedAt.Unix())

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *TemplateDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.AtLeastOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("name"),
		),
	}
}
