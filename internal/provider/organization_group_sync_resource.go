package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &OrganizationGroupSyncResource{}
var _ resource.ResourceWithImportState = &OrganizationGroupSyncResource{}

type OrganizationGroupSyncResource struct {
	*CoderdProviderData
}

// OrganizationGroupSyncResourceModel describes the resource data model.
type OrganizationGroupSyncResourceModel struct {
	OrganizationID    UUID         `tfsdk:"organization_id"`
	Field             types.String `tfsdk:"field"`
	RegexFilter       types.String `tfsdk:"regex_filter"`
	AutoCreateMissing types.Bool   `tfsdk:"auto_create_missing"`
	Mapping           types.Map    `tfsdk:"mapping"`
}

func NewOrganizationGroupSyncResource() resource.Resource {
	return &OrganizationGroupSyncResource{}
}

func (r *OrganizationGroupSyncResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_group_sync"
}

func (r *OrganizationGroupSyncResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Group sync settings for an organization on the Coder deployment. 
Multiple instances of this resource for a single organization will conflict.

~> **Warning**
This resource is only compatible with Coder version [2.16.0](https://github.com/coder/coder/releases/tag/v2.16.0) and later.
`,
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				CustomType:          UUIDType,
				Required:            true,
				MarkdownDescription: "The ID of the organization to configure group sync for.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"field": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The claim field that specifies what groups a user should be in.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"regex_filter": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "A regular expression that will be used to filter the groups " +
					"returned by the OIDC provider. Any group not matched will be ignored.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
					codersdkvalidator.Regexp(),
				},
			},
			"auto_create_missing": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Controls whether groups will be created if they are missing. Defaults to false.",
			},
			"mapping": schema.MapAttribute{
				ElementType:         types.ListType{ElemType: UUIDType},
				Required:            true,
				MarkdownDescription: "A map from OIDC group name to Coder group ID.",
			},
		},
	}
}

func (r *OrganizationGroupSyncResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrganizationGroupSyncResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationGroupSyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueUUID()

	groupSync, err := r.Client.GroupIDPSyncSettings(ctx, orgID.String())
	if err != nil {
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Organization with ID %q not found. Marking resource as deleted.", orgID.String()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization group sync settings, got error: %s", err))
		return
	}

	data.Field = types.StringValue(groupSync.Field)

	if groupSync.RegexFilter != nil {
		data.RegexFilter = types.StringValue(groupSync.RegexFilter.String())
	} else {
		data.RegexFilter = types.StringNull()
	}

	data.AutoCreateMissing = types.BoolValue(groupSync.AutoCreateMissing)

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
	data.Mapping = mapping

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationGroupSyncResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationGroupSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueUUID()

	tflog.Trace(ctx, "creating organization group sync", map[string]any{
		"organization_id": orgID,
		"field":           data.Field.ValueString(),
	})

	// Apply group sync settings
	resp.Diagnostics.Append(r.patchGroupSync(ctx, orgID, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationGroupSyncResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Read Terraform plan data into the model
	var data OrganizationGroupSyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueUUID()

	tflog.Trace(ctx, "updating organization group sync", map[string]any{
		"organization_id": orgID,
		"field":           data.Field.ValueString(),
	})

	resp.Diagnostics.Append(r.patchGroupSync(ctx, orgID, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationGroupSyncResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Read Terraform prior state data into the model
	var data OrganizationGroupSyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueUUID()

	tflog.Trace(ctx, "deleting organization group sync", map[string]any{
		"organization_id": orgID,
	})

	// Disable group sync by setting field to empty string
	var groupSync codersdk.GroupSyncSettings
	groupSync.Field = "" // Empty field disables group sync
	groupSync.AutoCreateMissing = false
	groupSync.Mapping = make(map[string][]uuid.UUID)
	groupSync.RegexFilter = nil

	// Perform the PATCH to disable group sync
	_, err := r.Client.PatchGroupIDPSyncSettings(ctx, orgID.String(), groupSync)
	if err != nil {
		if isNotFound(err) {
			// Organization doesn't exist, so group sync is already "deleted"
			return
		}
		resp.Diagnostics.AddError("Group Sync Delete error", err.Error())
		return
	}
}

func (r *OrganizationGroupSyncResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import using organization ID
	resource.ImportStatePassthroughID(ctx, path.Root("organization_id"), req, resp)
}

func (r *OrganizationGroupSyncResource) patchGroupSync(
	ctx context.Context,
	orgID uuid.UUID,
	data OrganizationGroupSyncResourceModel,
) diag.Diagnostics {
	var diags diag.Diagnostics

	var groupSync codersdk.GroupSyncSettings
	groupSync.Field = data.Field.ValueString()

	if !data.RegexFilter.IsNull() {
		groupSync.RegexFilter = regexp.MustCompile(data.RegexFilter.ValueString())
	}

	groupSync.AutoCreateMissing = data.AutoCreateMissing.ValueBool()
	groupSync.Mapping = make(map[string][]uuid.UUID)

	// Mapping is required, so always process it (can be empty)
	// Terraform doesn't know how to turn one our `UUID` Terraform values into a
	// `uuid.UUID`, so we have to do the unwrapping manually here.
	var mapping map[string][]UUID
	diags.Append(data.Mapping.ElementsAs(ctx, &mapping, false)...)
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
