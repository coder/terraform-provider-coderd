package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/coderd/util/slice"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &OrganizationResource{}
var _ resource.ResourceWithImportState = &OrganizationResource{}

type OrganizationResource struct {
	*CoderdProviderData
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

func NewOrganizationResource() resource.Resource {
	return &OrganizationResource{}
}

func (r *OrganizationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *OrganizationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An organization on the Coder deployment",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				CustomType:          UUIDType,
				Computed:            true,
				MarkdownDescription: "Organization ID",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Username of the organization.",
				Required:            true,
				Validators: []validator.String{
					codersdkvalidator.Name(),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the organization. Defaults to name.",
				Computed:            true,
				Optional:            true,
				Validators: []validator.String{
					codersdkvalidator.DisplayName(),
				},
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
			"Unable to configure provider data",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.CoderdProviderData = data
}

func (r *OrganizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.ID.ValueUUID()
	org, err := r.Client.Organization(ctx, orgID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by ID, got error: %s", err))
		return
	}

	// We've fetched the organization ID from state, and the latest values for
	// everything else from the backend. Ensure that any mutable data is synced
	// with the backend.
	data.Name = types.StringValue(org.Name)
	data.DisplayName = types.StringValue(org.DisplayName)
	data.Description = types.StringValue(org.Description)
	data.Icon = types.StringValue(org.Icon)
	if !data.Members.IsNull() {
		members, err := r.Client.OrganizationMembers(ctx, orgID)
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

func (r *OrganizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "creating organization")
	org, err := r.Client.CreateOrganization(ctx, codersdk.CreateOrganizationRequest{
		Name:        data.Name.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
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
	// Fill in `ID` since it must be "computed".
	data.ID = UUIDValue(org.ID)
	// We also fill in  `DisplayName`, since it's optional but the backend will
	// default it.
	data.DisplayName = types.StringValue(org.DisplayName)

	// Only configure members if they're specified
	if !data.Members.IsNull() {
		tflog.Trace(ctx, "setting organization members")
		var members []UUID
		resp.Diagnostics.Append(data.Members.ElementsAs(ctx, &members, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		for _, memberID := range members {
			_, err = r.Client.PostOrganizationMember(ctx, org.ID, memberID.ValueString())
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add member %s to organization %s, got error: %s", memberID, org.ID, err))
				return
			}
		}

		// Coder adds the user who creates the organization by default, but we may
		// actually be connected as a user who isn't in the list of members. If so
		// we should remove them!
		me, err := r.Client.User(ctx, codersdk.Me)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get current user, got error: %s", err))
			return
		}
		if !slice.Contains(members, UUIDValue(me.ID)) {
			err = r.Client.DeleteOrganizationMember(ctx, org.ID, codersdk.Me)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete self from new organization: %s", err))
				return
			}
		}

		tflog.Trace(ctx, "successfully set organization members")
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.ID.ValueUUID()

	// Update the organization metadata
	tflog.Trace(ctx, "updating organization", map[string]any{
		"id":               orgID,
		"new_name":         data.Name,
		"new_display_name": data.DisplayName,
		"new_description":  data.Description,
		"new_icon":         data.Icon,
	})
	_, err := r.Client.UpdateOrganization(ctx, orgID.String(), codersdk.UpdateOrganizationRequest{
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

	// If the organization membership is managed, update them.
	if !data.Members.IsNull() {
		orgMembers, err := r.Client.OrganizationMembers(ctx, orgID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization members , got error: %s", err))
			return
		}
		currentMembers := make([]uuid.UUID, 0, len(orgMembers))
		for _, member := range orgMembers {
			currentMembers = append(currentMembers, member.UserID)
		}

		var plannedMembers []UUID
		resp.Diagnostics.Append(data.Members.ElementsAs(ctx, &plannedMembers, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		add, remove := memberDiff(currentMembers, plannedMembers)
		tflog.Trace(ctx, "updating organization members", map[string]any{
			"new_members":     add,
			"removed_members": remove,
		})
		for _, memberID := range add {
			_, err := r.Client.PostOrganizationMember(ctx, orgID, memberID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add member %s to organization %s, got error: %s", memberID, orgID, err))
				return
			}
		}
		for _, memberID := range remove {
			err := r.Client.DeleteOrganizationMember(ctx, orgID, memberID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove member %s from organization %s, got error: %s", memberID, orgID, err))
				return
			}
		}
		tflog.Trace(ctx, "successfully updated organization members")
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.ID.ValueUUID()

	tflog.Trace(ctx, "deleting organization", map[string]any{
		"id": orgID,
	})
	err := r.Client.DeleteOrganization(ctx, orgID.String())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization %s, got error: %s", orgID, err))
		return
	}
	tflog.Trace(ctx, "successfully deleted organization")

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Terraform will eventually `Read` in the rest of the fields after we have
	// set the `id` attribute.
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
