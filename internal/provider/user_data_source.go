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
var _ datasource.DataSource = &UserDataSource{}
var _ datasource.DataSourceWithConfigValidators = &UserDataSource{}

func NewUserDataSource() datasource.DataSource {
	return &UserDataSource{}
}

// UserDataSource defines the data source implementation.
type UserDataSource struct {
	data *CoderdProviderData
}

// UserDataSourceModel describes the data source data model.
type UserDataSourceModel struct {
	// Username or ID must be set
	ID       UUID         `tfsdk:"id"`
	Username types.String `tfsdk:"username"`

	Name            types.String `tfsdk:"name"`
	Email           types.String `tfsdk:"email"`
	Roles           types.Set    `tfsdk:"roles"`      // owner, template-admin, user-admin, auditor (member is implicit)
	LoginType       types.String `tfsdk:"login_type"` // none, password, github, oidc
	Suspended       types.Bool   `tfsdk:"suspended"`
	AvatarURL       types.String `tfsdk:"avatar_url"`
	OrganizationIDs types.Set    `tfsdk:"organization_ids"`
	CreatedAt       types.Int64  `tfsdk:"created_at"` // Unix timestamp
	LastSeenAt      types.Int64  `tfsdk:"last_seen_at"`
}

func (d *UserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *UserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing user on the Coder deployment",

		// Validation handled by ConfigValidators
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				CustomType:          UUIDType,
				MarkdownDescription: "The ID of the user to retrieve. This field will be populated if a username is supplied.",
				Optional:            true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "The username of the user to retrieve. This field will be populated if an ID is supplied.",
				Optional:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name of the user.",
				Computed:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Email of the user.",
				Computed:            true,
			},
			"roles": schema.SetAttribute{
				MarkdownDescription: "Roles assigned to the user. Valid roles are `owner`, `template-admin`, `user-admin`, and `auditor`.",
				Computed:            true,
				ElementType:         types.StringType,
			},
			"login_type": schema.StringAttribute{
				MarkdownDescription: "Type of login for the user. Valid types are `none`, `password', `github`, and `oidc`.",
				Computed:            true,
			},
			"suspended": schema.BoolAttribute{
				MarkdownDescription: "Whether the user is suspended.",
				Computed:            true,
			},
			"avatar_url": schema.StringAttribute{
				MarkdownDescription: "URL of the user's avatar.",
				Computed:            true,
			},
			"organization_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of organizations the user is associated with.",
				Computed:            true,
				ElementType:         UUIDType,
			},
			"created_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp of when the user was created.",
				Computed:            true,
			},
			"last_seen_at": schema.Int64Attribute{
				MarkdownDescription: "Unix timestamp of when the user was last seen.",
				Computed:            true,
			},
		},
	}
}

func (d *UserDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data UserDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := d.data.Client

	var ident string
	if !data.ID.IsNull() {
		ident = data.ID.ValueString()
	} else {
		ident = data.Username.ValueString()
	}
	user, err := client.User(ctx, ident)
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("User with identifier %q not found. Marking as deleted.", ident))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}
	if len(user.OrganizationIDs) < 1 {
		resp.Diagnostics.AddError("Client Error", "User is not associated with any organizations")
		return
	}
	if !data.ID.IsNull() && user.ID != data.ID.ValueUUID() {
		resp.Diagnostics.AddError("Client Error", "Retrieved User's ID does not match the provided ID")
		return
	} else if !data.Username.IsNull() && user.Username != data.Username.ValueString() {
		resp.Diagnostics.AddError("Client Error", "Retrieved User's username does not match the provided username")
		return
	}

	data.ID = UUIDValue(user.ID)
	data.Username = types.StringValue(user.Username)
	data.Name = types.StringValue(user.Name)
	data.Email = types.StringValue(user.Email)
	roles := make([]attr.Value, 0, len(user.Roles))
	for _, role := range user.Roles {
		roles = append(roles, types.StringValue(role.Name))
	}
	data.Roles = types.SetValueMust(types.StringType, roles)
	data.LoginType = types.StringValue(string(user.LoginType))
	data.Suspended = types.BoolValue(user.Status == codersdk.UserStatusSuspended)

	orgIDs := make([]attr.Value, 0, len(user.OrganizationIDs))
	for _, orgID := range user.OrganizationIDs {
		orgIDs = append(orgIDs, UUIDValue(orgID))
	}
	data.OrganizationIDs = types.SetValueMust(UUIDType, orgIDs)
	data.CreatedAt = types.Int64Value(user.CreatedAt.Unix())
	data.LastSeenAt = types.Int64Value(user.LastSeenAt.Unix())

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *UserDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.AtLeastOneOf(
			path.MatchRoot("id"),
			path.MatchRoot("username"),
		),
	}
}
