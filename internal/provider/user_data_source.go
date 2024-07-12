// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

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
	ID       types.String `tfsdk:"id"`
	Username types.String `tfsdk:"username"`

	Name      types.String `tfsdk:"name"`
	Email     types.String `tfsdk:"email"`
	Roles     types.Set    `tfsdk:"roles"`      // owner, template-admin, user-admin, auditor (member is implicit)
	LoginType types.String `tfsdk:"login_type"` // none, password, github, oidc
	Suspended types.Bool   `tfsdk:"suspended"`
}

func (d *UserDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *UserDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing user on the coder deployment",

		// Validation handled by ConfigValidators
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the user to retrieve. This field will be populated if a username is supplied",
				Optional:            true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "The username of the user to retrieve. This field will be populated if an ID is supplied",
				Optional:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Email of the user.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name of the user. Defaults to username.",
				Computed:            true,
			},
			"roles": schema.SetAttribute{
				MarkdownDescription: "Roles assigned to the user. Valid roles are 'owner', 'template-admin', 'user-admin', and 'auditor'.",
				Computed:            true,
				ElementType:         types.StringType,
				// Validators: []validator.Set{
				// 	setvalidator.ValueStringsAre(
				// 		stringvalidator.OneOf("owner", "template-admin", "user-admin", "auditor"),
				// 	),
				// },
			},
			"login_type": schema.StringAttribute{
				MarkdownDescription: "Type of login for the user. Valid types are 'none', 'password', 'github', and 'oidc'.",
				Computed:            true,
				// Validators: []validator.String{
				// 	stringvalidator.OneOf("none", "password", "github", "oidc"),
				// },
			},
			"suspended": schema.BoolAttribute{
				MarkdownDescription: "Whether the user is suspended.",
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
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}
	if len(user.OrganizationIDs) < 1 {
		resp.Diagnostics.AddError("Client Error", "User is not associated with any organizations")
		return
	}

	data.ID = types.StringValue(user.ID.String())
	data.Email = types.StringValue(user.Email)
	data.Username = types.StringValue(user.Username)
	data.Name = types.StringValue(user.Name)
	roles := make([]attr.Value, 0, len(user.Roles))
	for _, role := range user.Roles {
		roles = append(roles, types.StringValue(role.Name))
	}
	data.Roles = types.SetValueMust(types.StringType, roles)
	data.LoginType = types.StringValue(string(user.LoginType))
	data.Suspended = types.BoolValue(user.Status == codersdk.UserStatusSuspended)

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
