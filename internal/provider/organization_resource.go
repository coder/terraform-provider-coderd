// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/coderd/util/slice"
	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &OrganizationResource{}
var _ resource.ResourceWithImportState = &OrganizationResource{}

func NewOrganizationResource() resource.Resource {
	return &OrganizationResource{}
}

// OrganizationResource defines the resource implementation.
type OrganizationResource struct {
	data *CoderdProviderData
}

// OrganizationResourceModel describes the resource data model.
type OrganizationResourceModel struct {
	ID UUID `tfsdk:"id"`

	Name        types.String `tfsdk:"name"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	Icon        types.String `tfsdk:"icon"`
	Members     types.Set    `tfsdk:"members"`
}

func (r *OrganizationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *OrganizationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An organization on the coder deployment.",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				CustomType: UUIDType,
				Computed:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
			},
			"display_name": schema.StringAttribute{
				Optional: true,
				Computed: true,
			},
			"description": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
			},
			"icon": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(""),
			},
			"members": schema.SetAttribute{
				MarkdownDescription: "Members of the organization, by ID. If null, members will not be added or removed by Terraform.",
				ElementType:         UUIDType,
				Optional:            true,
			},
			// TODO: Custom roles, premium license gated
		},
	}
}

func (r *OrganizationResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrganizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	displayName := data.Name.ValueString()
	if data.DisplayName.ValueString() != "" {
		displayName = data.DisplayName.ValueString()
	}

	tflog.Trace(ctx, "creating organization")
	org, err := client.CreateOrganization(ctx, codersdk.CreateOrganizationRequest{
		Name:        data.Name.ValueString(),
		DisplayName: displayName,
		Description: data.Description.ValueString(),
		Icon:        data.Icon.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to create organization", err.Error())
		return
	}
	tflog.Trace(ctx, "successfully created organization", map[string]any{
		"id": org.ID,
	})
	data.ID = UUIDValue(org.ID)
	data.DisplayName = types.StringValue(org.DisplayName)

	tflog.Trace(ctx, "setting organization members")
	var members []UUID
	resp.Diagnostics.Append(data.Members.ElementsAs(ctx, &members, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for _, memberID := range members {
		_, err = client.PostOrganizationMember(ctx, org.ID, memberID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add member %s to organization %s, got error: %s", memberID, org.ID, err))
			return
		}
	}

	me, err := client.User(ctx, codersdk.Me)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
		return
	}

	// If the logged-in user isn't in the members list, remove them from the organization (as they were added by default)
	// Ideally, future Coder versions won't add the logged-in user by default.
	if !slice.Contains(members, UUIDValue(me.ID)) {
		err = client.DeleteOrganizationMember(ctx, org.ID, codersdk.Me)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete self from new organization: %s", err))
		}
	}

	tflog.Trace(ctx, "successfully set organization members")
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	orgID := data.ID.ValueUUID()
	org, err := client.Organization(ctx, orgID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by ID, got error: %s", err))
	}

	data.Name = types.StringValue(org.Name)
	data.DisplayName = types.StringValue(org.DisplayName)
	data.Description = types.StringValue(org.Description)
	data.Icon = types.StringValue(org.Icon)
	if !data.Members.IsNull() {
		members, err := client.OrganizationMembers(ctx, orgID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization members, got error: %s", err))
			return
		}
		memberIDs := make([]attr.Value, 0, len(members))
		for _, member := range members {
			memberIDs = append(memberIDs, UUIDValue(member.UserID))
		}
		data.Members = types.SetValueMust(UUIDType, memberIDs)
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data OrganizationResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	orgID := data.ID.ValueUUID()

	orgMembers, err := client.OrganizationMembers(ctx, orgID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization members , got error: %s", err))
		return
	}

	if !data.Members.IsNull() {
		var plannedMembers []UUID
		resp.Diagnostics.Append(data.Members.ElementsAs(ctx, &plannedMembers, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		curMembers := make([]uuid.UUID, 0, len(orgMembers))
		for _, member := range orgMembers {
			curMembers = append(curMembers, member.UserID)
		}
		add, remove := memberDiff(curMembers, plannedMembers)
		tflog.Trace(ctx, "updating organization members", map[string]any{
			"new_members":     add,
			"removed_members": remove,
		})
		for _, memberID := range add {
			_, err := client.PostOrganizationMember(ctx, orgID, memberID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add member %s to organization %s, got error: %s", memberID, orgID, err))
				return
			}
		}
		for _, memberID := range remove {
			err := client.DeleteOrganizationMember(ctx, orgID, memberID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove member %s from organization %s, got error: %s", memberID, orgID, err))
				return
			}
		}
		tflog.Trace(ctx, "successfully updated organization members")
	}

	tflog.Trace(ctx, "updating organization", map[string]any{
		"id":               orgID,
		"new_name":         data.Name,
		"new_display_name": data.DisplayName,
		"new_description":  data.Description,
		"new_icon":         data.Icon,
	})
	_, err = client.UpdateOrganization(ctx, orgID.String(), codersdk.UpdateOrganizationRequest{
		Name:        data.Name.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
		Description: data.Description.ValueStringPointer(),
		Icon:        data.Icon.ValueStringPointer(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization %s, got error: %s", orgID, err))
		return
	}
	tflog.Trace(ctx, "successfully updated organization")

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	orgID := data.ID.ValueUUID()

	tflog.Trace(ctx, "deleting organization", map[string]any{
		"id": orgID,
	})

	err := client.DeleteOrganization(ctx, orgID.String())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization %s, got error: %s", orgID, err))
		return
	}
	tflog.Trace(ctx, "successfully deleted organization")

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
