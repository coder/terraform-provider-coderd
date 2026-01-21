package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &OrganizationDataSource{}
var _ datasource.DataSourceWithConfigValidators = &OrganizationDataSource{}

func NewOrganizationDataSource() datasource.DataSource {
	return &OrganizationDataSource{}
}

// OrganizationDataSource defines the data source implementation.
type OrganizationDataSource struct {
	data *CoderdProviderData
}

// OrganizationDataSourceModel describes the data source data model.
type OrganizationDataSourceModel struct {
	// Exactly one of ID, IsDefault, or Name must be set.
	ID        UUID         `tfsdk:"id"`
	IsDefault types.Bool   `tfsdk:"is_default"`
	Name      types.String `tfsdk:"name"`

	CreatedAt        types.Int64  `tfsdk:"created_at"`
	UpdatedAt        types.Int64  `tfsdk:"updated_at"`
	WorkspaceSharing types.String `tfsdk:"workspace_sharing"`
	// TODO: This could reasonably store some User object - though we may need to make additional queries depending on what fields we
	// want, or to have one consistent user type for all data sources.
	Members types.Set `tfsdk:"members"`
}

func (d *OrganizationDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (d *OrganizationDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `An existing organization on the Coder deployment.

~> **Warning**
This data source is only compatible with Coder version [2.13.0](https://github.com/coder/coder/releases/tag/v2.13.0) and later.
`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the organization to retrieve. This field will be populated if the organization is found by name, or if the default organization is requested.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
			},
			"is_default": schema.BoolAttribute{
				MarkdownDescription: "Whether the organization is the default organization of the deployment. This field will be populated if the organization is found by ID or name.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the organization to retrieve. This field will be populated if the organization is found by ID, or if the default organization is requested.",
				Optional:            true,
				Computed:            true,
			},
			"created_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp when the organization was created.",
				Computed:            true,
			},
			"updated_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp when the organization was last updated.",
				Computed:            true,
			},
			"workspace_sharing": schema.StringAttribute{
				MarkdownDescription: "Workspace sharing setting for the organization. " +
					"Valid values are `everyone` and `none`.",
				Computed: true,
			},

			"members": schema.SetAttribute{
				MarkdownDescription: "Members of the organization, by ID",
				Computed:            true,
				ElementType:         UUIDType,
			},
		},
	}
}

func (d *OrganizationDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrganizationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := d.data.Client

	var (
		org codersdk.Organization
		err error
	)
	if !data.ID.IsNull() { // By ID
		orgID := data.ID.ValueUUID()
		org, err = client.Organization(ctx, orgID)
		if err != nil {
			if isNotFound(err) {
				resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Organization with ID %s not found. Marking as deleted.", data.ID.ValueString()))
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by ID, got error: %s", err))
			return
		}
		if org.ID != data.ID.ValueUUID() {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Organization ID %s does not match requested ID %s", org.ID, data.ID))
			return
		}
	} else if data.IsDefault.ValueBool() { // Get Default
		org, err = client.OrganizationByName(ctx, "default")
		if err != nil {
			if isNotFound(err) {
				resp.Diagnostics.AddWarning("Client Warning", "Default organization not found. Marking as deleted.")
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get default organization, got error: %s", err))
			return
		}
		if !org.IsDefault {
			resp.Diagnostics.AddError("Client Error", "Found organization was not the default organization")
			return
		}
	} else { // By Name
		org, err = client.OrganizationByName(ctx, data.Name.ValueString())
		if err != nil {
			if isNotFound(err) {
				resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Organization with name %s not found. Marking as deleted.", data.Name))
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by name, got error: %s", err))
			return
		}
		if org.Name != data.Name.ValueString() {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Organization name %s does not match requested name %s", org.Name, data.Name))
			return
		}
	}
	data.ID = UUIDValue(org.ID)
	data.Name = types.StringValue(org.Name)
	data.IsDefault = types.BoolValue(org.IsDefault)
	data.CreatedAt = types.Int64Value(org.CreatedAt.Unix())
	data.UpdatedAt = types.Int64Value(org.UpdatedAt.Unix())
	workspaceSharingSettings, err := client.WorkspaceSharingSettings(ctx, org.ID.String())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get workspace sharing settings, got error: %s", err))
		return
	}
	data.WorkspaceSharing = types.StringValue(workspaceSharingValueFromSettings(workspaceSharingSettings))
	members, err := client.OrganizationMembers(ctx, org.ID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization members, got error: %s", err))
		return
	}
	memberIDs := make([]attr.Value, 0, len(members))
	for _, member := range members {
		memberIDs = append(memberIDs, UUIDValue(member.UserID))
	}
	data.Members = types.SetValueMust(UUIDType, memberIDs)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *OrganizationDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("is_default"),
			path.MatchRoot("name"),
		),
	}
}
