package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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

	GroupSync types.Object `tfsdk:"group_sync"`
	RoleSync  types.Object `tfsdk:"role_sync"`
}

type GroupSyncModel struct {
	Field             types.String `tfsdk:"field"`
	RegexFilter       types.String `tfsdk:"regex_filter"`
	AutoCreateMissing types.Bool   `tfsdk:"auto_create_missing"`
	Mapping           types.Map    `tfsdk:"mapping"`
}

var groupSyncAttrTypes = map[string]attr.Type{
	"field":               types.StringType,
	"regex_filter":        types.StringType,
	"auto_create_missing": types.BoolType,
	"mapping":             types.MapType{ElemType: types.ListType{ElemType: UUIDType}},
}

func (m GroupSyncModel) ValueObject() types.Object {
	return types.ObjectValueMust(groupSyncAttrTypes, map[string]attr.Value{
		"field":               m.Field,
		"regex_filter":        m.RegexFilter,
		"auto_create_missing": m.AutoCreateMissing,
		"mapping":             m.Mapping,
	})
}

type RoleSyncModel struct {
	Field   types.String `tfsdk:"field"`
	Mapping types.Map    `tfsdk:"mapping"`
}

var roleSyncAttrTypes = map[string]attr.Type{
	"field":   types.StringType,
	"mapping": types.MapType{ElemType: types.ListType{ElemType: types.StringType}},
}

func (m RoleSyncModel) ValueObject() types.Object {
	return types.ObjectValueMust(roleSyncAttrTypes, map[string]attr.Value{
		"field":   m.Field,
		"mapping": m.Mapping,
	})
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
				MarkdownDescription: "Name of the organization.",
				Required:            true,
				Validators: []validator.String{
					codersdkvalidator.Name(),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "Display name of the organization. Defaults to name.",
				Computed:            true,
				Optional:            true,
				Default:             stringdefault.StaticString(""),
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
		},

		Blocks: map[string]schema.Block{
			"group_sync": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{
					"field": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "The claim field that specifies what groups " +
							"a user should be in.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"regex_filter": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "A regular expression that will be used to " +
							"filter the groups returned by the OIDC provider. Any group " +
							"not matched will be ignored.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"auto_create_missing": schema.BoolAttribute{
						Optional: true,
						MarkdownDescription: "Controls whether groups will be created if " +
							"they are missing.",
					},
					"mapping": schema.MapAttribute{
						ElementType:         types.ListType{ElemType: UUIDType},
						Optional:            true,
						MarkdownDescription: "A map from OIDC group name to Coder group ID.",
					},
				},
			},
			"role_sync": schema.SingleNestedBlock{
				Attributes: map[string]schema.Attribute{
					"field": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "The claim field that specifies what " +
							"organization roles a user should be given.",
						Validators: []validator.String{
							stringvalidator.LengthAtLeast(1),
						},
					},
					"mapping": schema.MapAttribute{
						ElementType: types.ListType{ElemType: types.StringType},
						Optional:    true,
						MarkdownDescription: "A map from OIDC group name to Coder " +
							"organization role.",
					},
				},
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

	var org codersdk.Organization
	var err error
	if data.ID.IsNull() {
		orgName := data.Name.ValueString()
		org, err = r.Client.OrganizationByName(ctx, orgName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by name, got error: %s", err))
			return
		}
		data.ID = UUIDValue(org.ID)
	} else {
		orgID := data.ID.ValueUUID()
		org, err = r.Client.Organization(ctx, orgID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization by ID, got error: %s", err))
			return
		}
	}

	if !data.GroupSync.IsNull() {
		groupSync, err := r.Client.GroupIDPSyncSettings(ctx, data.ID.ValueUUID().String())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("unable to get organization group sync settings, got error: %s", err))
			return
		}

		// Read values from Terraform
		var groupSyncData GroupSyncModel
		resp.Diagnostics.Append(data.GroupSync.As(ctx, &groupSyncData, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}

		if !groupSyncData.Field.IsNull() {
			groupSyncData.Field = types.StringValue(groupSync.Field)
		}
		if !groupSyncData.RegexFilter.IsNull() {
			groupSyncData.RegexFilter = types.StringValue(groupSync.RegexFilter.String())
		}
		if !groupSyncData.AutoCreateMissing.IsNull() {
			groupSyncData.AutoCreateMissing = types.BoolValue(groupSync.AutoCreateMissing)
		}
		if !groupSyncData.Mapping.IsNull() {
			elements := make(map[string][]string)
			for key, ids := range groupSync.Mapping {
				for _, id := range ids {
					elements[key] = append(elements[key], id.String())
				}
			}

			mapping, diags := types.MapValueFrom(ctx, types.ListType{ElemType: UUIDType}, elements)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			groupSyncData.Mapping = mapping
		}

		data.GroupSync = groupSyncData.ValueObject()
	}

	if !data.RoleSync.IsNull() {
		roleSync, err := r.Client.RoleIDPSyncSettings(ctx, data.ID.ValueUUID().String())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("unable to get organization role sync settings, got error: %s", err))
			return
		}

		// Read values from Terraform
		var roleSyncData RoleSyncModel
		resp.Diagnostics.Append(data.RoleSync.As(ctx, &roleSyncData, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}

		if !roleSyncData.Field.IsNull() {
			roleSyncData.Field = types.StringValue(roleSync.Field)
		}
		if !roleSyncData.Mapping.IsNull() {
			mapping, diags := types.MapValueFrom(ctx, types.ListType{ElemType: types.StringType}, roleSync.Mapping)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			roleSyncData.Mapping = mapping
		}

		data.RoleSync = roleSyncData.ValueObject()
	}

	// We've fetched the organization ID from state, and the latest values for
	// everything else from the backend. Ensure that any mutable data is synced
	// with the backend.
	data.Name = types.StringValue(org.Name)
	data.DisplayName = types.StringValue(org.DisplayName)
	data.Description = types.StringValue(org.Description)
	data.Icon = types.StringValue(org.Icon)

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

	tflog.Trace(ctx, "creating organization", map[string]any{
		"id":           data.ID.ValueUUID(),
		"name":         data.Name.ValueString(),
		"display_name": data.DisplayName.ValueString(),
		"description":  data.Description.ValueString(),
		"icon":         data.Icon.ValueString(),
	})
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
		"id":           org.ID,
		"name":         org.Name,
		"display_name": org.DisplayName,
		"description":  org.Description,
		"icon":         org.Icon,
	})
	// Fill in `ID` since it must be "computed".
	data.ID = UUIDValue(org.ID)
	// We also fill in  `DisplayName`, since it's optional but the backend will
	// default it.
	data.DisplayName = types.StringValue(org.DisplayName)

	// Now apply group and role sync settings, if specified
	orgID := data.ID.ValueUUID()
	tflog.Trace(ctx, "updating group sync", map[string]any{
		"orgID": orgID,
	})
	if !data.GroupSync.IsNull() {
		resp.Diagnostics.Append(r.patchGroupSync(ctx, orgID, data.GroupSync)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	tflog.Trace(ctx, "updating role sync", map[string]any{
		"orgID": orgID,
	})
	if !data.RoleSync.IsNull() {
		resp.Diagnostics.Append(r.patchRoleSync(ctx, orgID, data.RoleSync)...)
		if resp.Diagnostics.HasError() {
			return
		}
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
		"new_name":         data.Name.ValueString(),
		"new_display_name": data.DisplayName.ValueString(),
		"new_description":  data.Description.ValueString(),
		"new_icon":         data.Icon.ValueString(),
	})
	org, err := r.Client.UpdateOrganization(ctx, orgID.String(), codersdk.UpdateOrganizationRequest{
		Name:        data.Name.ValueString(),
		DisplayName: data.DisplayName.ValueString(),
		Description: data.Description.ValueStringPointer(),
		Icon:        data.Icon.ValueStringPointer(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization %s, got error: %s", orgID, err))
		return
	}

	tflog.Trace(ctx, "successfully updated organization", map[string]any{
		"id":           orgID,
		"name":         org.Name,
		"display_name": org.DisplayName,
		"description":  org.Description,
		"icon":         org.Icon,
	})

	tflog.Trace(ctx, "updating group sync", map[string]any{
		"orgID": orgID,
	})
	if !data.GroupSync.IsNull() {
		resp.Diagnostics.Append(r.patchGroupSync(ctx, orgID, data.GroupSync)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	tflog.Trace(ctx, "updating role sync", map[string]any{
		"orgID": orgID,
	})
	if !data.RoleSync.IsNull() {
		resp.Diagnostics.Append(r.patchRoleSync(ctx, orgID, data.RoleSync)...)
		if resp.Diagnostics.HasError() {
			return
		}
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
		"id":   orgID,
		"name": data.Name.ValueString(),
	})
	err := r.Client.DeleteOrganization(ctx, orgID.String())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization %s, got error: %s", orgID, err))
		return
	}
	tflog.Trace(ctx, "successfully deleted organization", map[string]any{
		"id":   orgID,
		"name": data.Name.ValueString(),
	})

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Terraform will eventually `Read` in the rest of the fields after we have
	// set the `name` attribute.
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

func (r *OrganizationResource) patchGroupSync(
	ctx context.Context,
	orgID uuid.UUID,
	groupSyncObject types.Object,
) diag.Diagnostics {
	var diags diag.Diagnostics

	// Read values from Terraform
	var groupSyncData GroupSyncModel
	diags.Append(groupSyncObject.As(ctx, &groupSyncData, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	// Convert that into the type used to send the PATCH to the backend
	var groupSync codersdk.GroupSyncSettings
	groupSync.Field = groupSyncData.Field.ValueString()
	groupSync.RegexFilter = regexp.MustCompile(groupSyncData.RegexFilter.ValueString())
	groupSync.AutoCreateMissing = groupSyncData.AutoCreateMissing.ValueBool()
	groupSync.Mapping = make(map[string][]uuid.UUID)
	// Terraform doesn't know how to turn one our `UUID` Terraform values into a
	// `uuid.UUID`, so we have to do the unwrapping manually here.
	var mapping map[string][]UUID
	diags.Append(groupSyncData.Mapping.ElementsAs(ctx, &mapping, false)...)
	if diags.HasError() {
		return diags
	}
	for key, ids := range mapping {
		for _, id := range ids {
			groupSync.Mapping[key] = append(groupSync.Mapping[key], id.ValueUUID())
		}
	}

	// Perform the PATCH
	_, err := r.Client.PatchGroupIDPSyncSettings(ctx, orgID.String(), groupSync)
	if err != nil {
		diags.AddError("Group Sync Update error", err.Error())
		return diags
	}

	return diags
}

func (r *OrganizationResource) patchRoleSync(
	ctx context.Context,
	orgID uuid.UUID,
	roleSyncObject types.Object,
) diag.Diagnostics {
	var diags diag.Diagnostics

	// Read values from Terraform
	var roleSyncData RoleSyncModel
	diags.Append(roleSyncObject.As(ctx, &roleSyncData, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	// Convert that into the type used to send the PATCH to the backend
	var roleSync codersdk.RoleSyncSettings
	roleSync.Field = roleSyncData.Field.ValueString()
	diags.Append(roleSyncData.Mapping.ElementsAs(ctx, &roleSync.Mapping, false)...)
	if diags.HasError() {
		return diags
	}

	// Perform the PATCH
	_, err := r.Client.PatchRoleIDPSyncSettings(ctx, orgID.String(), roleSync)
	if err != nil {
		diags.AddError("Role Sync Update error", err.Error())
		return diags
	}

	return diags
}
