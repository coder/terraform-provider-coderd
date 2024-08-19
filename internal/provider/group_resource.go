package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &GroupResource{}
var _ resource.ResourceWithImportState = &GroupResource{}

func NewGroupResource() resource.Resource {
	return &GroupResource{}
}

// GroupResource defines the resource implementation.
type GroupResource struct {
	data *CoderdProviderData
}

// GroupResourceModel describes the resource data model.
type GroupResourceModel struct {
	ID UUID `tfsdk:"id"`

	Name           types.String `tfsdk:"name"`
	DisplayName    types.String `tfsdk:"display_name"`
	AvatarURL      types.String `tfsdk:"avatar_url"`
	QuotaAllowance types.Int32  `tfsdk:"quota_allowance"`
	OrganizationID UUID         `tfsdk:"organization_id"`
	Members        types.Set    `tfsdk:"members"`
}

func CheckGroupEntitlements(ctx context.Context, features map[codersdk.FeatureName]codersdk.Feature) (diags diag.Diagnostics) {
	if !features[codersdk.FeatureTemplateRBAC].Enabled {
		diags.AddError("Feature not enabled", "Your license is not entitled to use groups.")
		return
	}
	return nil
}

func (r *GroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *GroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A group on the Coder deployment. If you want to have a group resource with unmanaged members, but still want to read the members in Terraform, use the `data.coderd_group` data source. Creating groups requires an Enterprise license.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Group ID.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The unique name of the group.",
				Required:            true,
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "The display name of the group. Defaults to the group name.",
				Computed:            true,
				Optional:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				Default: stringdefault.StaticString(""),
			},
			"avatar_url": schema.StringAttribute{
				MarkdownDescription: "The URL of the group's avatar.",
				Computed:            true,
				Optional:            true,
				Default:             stringdefault.StaticString(""),
			},
			// Int32 in the db
			"quota_allowance": schema.Int32Attribute{
				MarkdownDescription: "The number of quota credits to allocate to each user in the group.",
				Optional:            true,
				Computed:            true,
				Default:             int32default.StaticInt32(0),
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The organization ID that the group belongs to. Defaults to the provider default organization ID.",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"members": schema.SetAttribute{
				MarkdownDescription: "Members of the group, by ID. If null, members will not be added or removed by Terraform.",
				ElementType:         UUIDType,
				Optional:            true,
			},
		},
	}
}

func (r *GroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
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

func (r *GroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GroupResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(CheckGroupEntitlements(ctx, r.data.Features)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	if data.OrganizationID.IsUnknown() {
		data.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	orgID := data.OrganizationID.ValueUUID()

	tflog.Info(ctx, "creating group")
	group, err := client.CreateGroup(ctx, orgID, codersdk.CreateGroupRequest{
		Name:           data.Name.ValueString(),
		DisplayName:    data.DisplayName.ValueString(),
		AvatarURL:      data.AvatarURL.ValueString(),
		QuotaAllowance: int(data.QuotaAllowance.ValueInt32()),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create group, got error: %s", err))
		return
	}
	tflog.Info(ctx, "successfully created group", map[string]any{
		"id": group.ID.String(),
	})
	data.ID = UUIDValue(group.ID)
	data.DisplayName = types.StringValue(group.DisplayName)

	tflog.Info(ctx, "setting group members")
	var members []string
	resp.Diagnostics.Append(
		data.Members.ElementsAs(ctx, &members, false)...,
	)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err = client.PatchGroup(ctx, group.ID, codersdk.PatchGroupRequest{
		AddUsers: members,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add members to group, got error: %s", err))
		return
	}
	tflog.Info(ctx, "successfully set group members")

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data GroupResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	groupID := data.ID.ValueUUID()

	group, err := client.Group(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get group, got error: %s", err))
		return
	}

	data.Name = types.StringValue(group.Name)
	data.DisplayName = types.StringValue(group.DisplayName)
	data.AvatarURL = types.StringValue(group.AvatarURL)
	data.QuotaAllowance = types.Int32Value(int32(group.QuotaAllowance))
	data.OrganizationID = UUIDValue(group.OrganizationID)
	if !data.Members.IsNull() {
		members := make([]attr.Value, 0, len(group.Members))
		for _, member := range group.Members {
			members = append(members, UUIDValue(member.ID))
		}
		data.Members = types.SetValueMust(UUIDType, members)
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GroupResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	if data.OrganizationID.IsUnknown() {
		data.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}
	groupID := data.ID.ValueUUID()

	group, err := client.Group(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get group, got error: %s", err))
		return
	}
	var add []string
	var remove []string
	if !data.Members.IsNull() {
		var plannedMembers []UUID
		resp.Diagnostics.Append(
			data.Members.ElementsAs(ctx, &plannedMembers, false)...,
		)
		if resp.Diagnostics.HasError() {
			return
		}
		curMembers := make([]uuid.UUID, 0, len(group.Members))
		for _, member := range group.Members {
			curMembers = append(curMembers, member.ID)
		}
		add, remove = memberDiff(curMembers, plannedMembers)
	}
	tflog.Info(ctx, "updating group", map[string]any{
		"id":              groupID,
		"new_members":     add,
		"removed_members": remove,
		"new_name":        data.Name,
		"new_displayname": data.DisplayName,
		"new_avatarurl":   data.AvatarURL,
		"new_quota":       data.QuotaAllowance,
	})

	quotaAllowance := int(data.QuotaAllowance.ValueInt32())
	_, err = client.PatchGroup(ctx, group.ID, codersdk.PatchGroupRequest{
		AddUsers:       add,
		RemoveUsers:    remove,
		Name:           data.Name.ValueString(),
		DisplayName:    data.DisplayName.ValueStringPointer(),
		AvatarURL:      data.AvatarURL.ValueStringPointer(),
		QuotaAllowance: &quotaAllowance,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update group, got error: %s", err))
		return
	}
	tflog.Info(ctx, "successfully updated group")

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GroupResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	groupID := data.ID.ValueUUID()

	tflog.Info(ctx, "deleting group", map[string]any{
		"id": groupID,
	})
	err := client.DeleteGroup(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete group, got error: %s", err))
		return
	}
	tflog.Info(ctx, "successfully deleted group")
}

func (r *GroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	client := r.data.Client
	groupID, err := uuid.Parse(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to parse import group ID as UUID, got error: %s", err))
		return
	}
	group, err := client.Group(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get imported group, got error: %s", err))
		return
	}
	if group.Source == "oidc" {
		resp.Diagnostics.AddError("Client Error", "Cannot import groups created via OIDC")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
