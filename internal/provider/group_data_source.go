package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &GroupDataSource{}

func NewGroupDataSource() datasource.DataSource {
	return &GroupDataSource{}
}

// GroupDataSource defines the data source implementation.
type GroupDataSource struct {
	*CoderdProviderData
}

// GroupDataSourceModel describes the data source data model.
type GroupDataSourceModel struct {
	// ID or name and organization ID must be set
	ID             UUID         `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	OrganizationID UUID         `tfsdk:"organization_id"`

	DisplayName    types.String `tfsdk:"display_name"`
	AvatarURL      types.String `tfsdk:"avatar_url"`
	QuotaAllowance types.Int32  `tfsdk:"quota_allowance"`
	Source         types.String `tfsdk:"source"`
	Members        []Member     `tfsdk:"members"`
}

type Member struct {
	ID              UUID         `tfsdk:"id"`
	Username        types.String `tfsdk:"username"`
	Email           types.String `tfsdk:"email"`
	CreatedAt       types.Int64  `tfsdk:"created_at"`
	LastSeenAt      types.Int64  `tfsdk:"last_seen_at"`
	Status          types.String `tfsdk:"status"`
	LoginType       types.String `tfsdk:"login_type"`
	ThemePreference types.String `tfsdk:"theme_preference"`
}

func (d *GroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (d *GroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing group on the Coder deployment.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the group to retrieve. This field will be populated if a name and organization ID is supplied.",
				Optional:            true,
				Computed:            true,
				CustomType:          UUIDType,
				Validators: []validator.String{
					stringvalidator.AtLeastOneOf(path.Expressions{
						path.MatchRoot("name"),
					}...),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the group to retrieve. This field will be populated if an ID is supplied.",
				Optional:            true,
				Computed:            true,
				Validators:          []validator.String{},
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The organization ID that the group belongs to. This field will be populated if an ID is supplied. Defaults to the provider default organization ID.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
			},
			"display_name": schema.StringAttribute{
				Computed: true,
			},
			"avatar_url": schema.StringAttribute{
				Computed: true,
			},
			"quota_allowance": schema.Int32Attribute{
				MarkdownDescription: "The number of quota credits to allocate to each user in the group.",
				Computed:            true,
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "The source of the group. Either `oidc` or `user`.",
				Computed:            true,
			},
			"members": schema.SetNestedAttribute{
				MarkdownDescription: "Members of the group.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							CustomType: UUIDType,
							Computed:   true,
						},
						"username": schema.StringAttribute{
							Computed: true,
						},
						"email": schema.StringAttribute{
							Computed: true,
						},
						"created_at": schema.Int64Attribute{
							MarkdownDescription: "Unix timestamp of when the member was created.",
							Computed:            true,
						},
						"last_seen_at": schema.Int64Attribute{
							MarkdownDescription: "Unix timestamp of when the member was last seen.",
							Computed:            true,
						},
						"status": schema.StringAttribute{
							MarkdownDescription: "The status of the member. Can be `active`, `dormant` or `suspended`.",
							Computed:            true,
						},
						"login_type": schema.StringAttribute{
							MarkdownDescription: "The login type of the member. Can be `oidc`, `token`, `password`, `github` or `none`.",
							Computed:            true,
						},
						"theme_preference": schema.StringAttribute{
							Computed: true,
						},
						// TODO: Upgrade requested user type if required
					},
				},
			},
		},
	}
}

func (d *GroupDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.CoderdProviderData = data
}

func (d *GroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	// Read Terraform configuration data into the model
	var data GroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(CheckGroupEntitlements(ctx, d.Features)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var (
		group codersdk.Group
		err   error
	)
	if !data.ID.IsNull() {
		groupID := data.ID.ValueUUID()
		group, err = r.Client.Group(ctx, groupID)
		if err != nil {
			if isNotFound(err) {
				resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Group with ID %s not found. Marking as deleted.", groupID.String()))
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get group by ID, got error: %s", err))
			return
		}
		data.Name = types.StringValue(group.Name)
		data.OrganizationID = UUIDValue(group.OrganizationID)
	} else {
		group, err = r.Client.GroupByOrgAndName(ctx, data.OrganizationID.ValueUUID(), data.Name.ValueString())
		if err != nil {
			if isNotFound(err) {
				resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Group with name %s not found in organization with ID %s. Marking as deleted.", data.Name.ValueString(), data.OrganizationID.ValueString()))
				resp.State.RemoveResource(ctx)
				return
			}
			resp.Diagnostics.AddError("Failed to get group by name and org ID", err.Error())
			return
		}
		data.ID = UUIDValue(group.ID)
	}

	data.DisplayName = types.StringValue(group.DisplayName)
	data.AvatarURL = types.StringValue(group.AvatarURL)
	data.QuotaAllowance = types.Int32Value(int32(group.QuotaAllowance))
	members := make([]Member, 0, len(group.Members))
	for _, member := range group.Members {
		members = append(members, Member{
			ID:              UUIDValue(member.ID),
			Username:        types.StringValue(member.Username),
			Email:           types.StringValue(member.Email),
			CreatedAt:       types.Int64Value(member.CreatedAt.Unix()),
			LastSeenAt:      types.Int64Value(member.LastSeenAt.Unix()),
			Status:          types.StringValue(string(member.Status)),
			LoginType:       types.StringValue(string(member.LoginType)),
			ThemePreference: types.StringValue(member.ThemePreference),
		})
	}
	data.Members = members
	data.Source = types.StringValue(string(group.Source))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
