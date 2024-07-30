package provider

import (
	"bufio"
	"context"
	"fmt"
	"io"

	"cdr.dev/slog"
	"github.com/coder/coder/cryptorand"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &TemplateResource{}
var _ resource.ResourceWithImportState = &TemplateResource{}
var _ resource.ResourceWithConfigValidators = &TemplateResource{}

func NewTemplateResource() resource.Resource {
	return &TemplateResource{}
}

// TemplateResource defines the resource implementation.
type TemplateResource struct {
	data *CoderdProviderData
}

// TemplateResourceModel describes the resource data model.
type TemplateResourceModel struct {
	ID UUID `tfsdk:"id"`

	Name               types.String `tfsdk:"name"`
	DisplayName        types.String `tfsdk:"display_name"`
	Description        types.String `tfsdk:"description"`
	OrganizationID     UUID         `tfsdk:"organization_id"`
	Icon               types.String `tfsdk:"icon"`
	AllowUserAutoStart types.Bool   `tfsdk:"allow_user_auto_start"`
	AllowUserAutoStop  types.Bool   `tfsdk:"allow_user_auto_stop"`

	// If null, we are not managing ACL via Terraform (such as for AGPL).
	ACL      types.Object `tfsdk:"acl"`
	Versions Versions     `tfsdk:"versions"`
}

// EqualTemplateMetadata returns true if two templates have identical metadata (excluding ACL).
func (m TemplateResourceModel) EqualTemplateMetadata(other TemplateResourceModel) bool {
	return m.Name.Equal(other.Name) &&
		m.DisplayName.Equal(other.DisplayName) &&
		m.Description.Equal(other.Description) &&
		m.OrganizationID.Equal(other.OrganizationID) &&
		m.Icon.Equal(other.Icon) &&
		m.AllowUserAutoStart.Equal(other.AllowUserAutoStart) &&
		m.AllowUserAutoStop.Equal(other.AllowUserAutoStop)
}

type TemplateVersion struct {
	ID UUID `tfsdk:"id"`

	NamePrefix         types.String `tfsdk:"name_prefix"`
	Message            types.String `tfsdk:"message"`
	Directory          types.String `tfsdk:"directory"`
	DirectoryHash      types.String `tfsdk:"directory_hash"`
	Active             types.Bool   `tfsdk:"active"`
	TerraformVariables []Variable   `tfsdk:"tf_vars"`
	ProvisionerTags    []Variable   `tfsdk:"provisioner_tags"`

	RevisionNum types.Int64  `tfsdk:"revision_num"`
	FullName    types.String `tfsdk:"full_name"`
}

type Versions []TemplateVersion

func (v Versions) ByNamePrefix(namePrefix types.String) *TemplateVersion {
	for _, m := range v {
		if m.NamePrefix.Equal(namePrefix) {
			return &m
		}
	}
	return nil
}

type Variable struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

var variableNestedObject = schema.NestedAttributeObject{
	Attributes: map[string]schema.Attribute{
		"name": schema.StringAttribute{
			Required: true,
		},
		"value": schema.StringAttribute{
			Required: true,
		},
	},
}

type ACL struct {
	UserPermissions  []Permission `tfsdk:"users"`
	GroupPermissions []Permission `tfsdk:"groups"`
}

// aclTypeAttr is the type schema for an instance of `ACL`.
var aclTypeAttr = map[string]attr.Type{
	"users":  permissionTypeAttr,
	"groups": permissionTypeAttr,
}

type Permission struct {
	// Purposefully left as a string so we can later support an `everyone` shortcut
	// identifier for the Everyone group.
	ID   types.String `tfsdk:"id"`
	Role types.String `tfsdk:"role"`
}

// permissionAttribute is the attribute schema for an instance of `[]Permission`.
var permissionAttribute = schema.SetNestedAttribute{
	Required: true,
	NestedObject: schema.NestedAttributeObject{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required: true,
			},
			"role": schema.StringAttribute{
				Required: true,
			},
		},
	},
}

// permissionTypeAttr is the type schema for an instance of `[]Permission`.
var permissionTypeAttr = basetypes.SetType{ElemType: types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"id":   basetypes.StringType{},
		"role": basetypes.StringType{},
	},
}}

func (r *TemplateResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_template"
}

func (r *TemplateResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Coder template",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The ID of the template.",
				CustomType:          UUIDType,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The name of the template.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 32),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "The display name of the template. Defaults to the template name.",
				Optional:            true,
				Computed:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description of the template.",
				Computed:            true,
				Optional:            true,
				Default:             stringdefault.StaticString(""),
			},
			"organization_id": schema.StringAttribute{
				MarkdownDescription: "The ID of the organization. Defaults to the provider's default organization",
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"icon": schema.StringAttribute{
				MarkdownDescription: "Relative path or external URL that specifes an icon to be displayed in the dashboard.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"allow_user_auto_start": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"allow_user_auto_stop": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
			},
			"acl": schema.SingleNestedAttribute{
				MarkdownDescription: "Access control list for the template. Requires an enterprise Coder deployment. If null, ACL policies will not be added or removed by Terraform.",
				Optional:            true,
				Attributes: map[string]schema.Attribute{
					"users":  permissionAttribute,
					"groups": permissionAttribute,
				},
			},
			"versions": schema.ListNestedAttribute{
				Required: true,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					NewVersionsValidator(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							CustomType: UUIDType,
							Computed:   true,
							// This ID may change as the version is re-created.
						},
						"name_prefix": schema.StringAttribute{
							MarkdownDescription: "A prefix for the name of the template version. Must be unique within the list of versions.",
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString(""),
							PlanModifiers: []planmodifier.String{
								stringplanmodifier.UseStateForUnknown(),
							},
						},
						"message": schema.StringAttribute{
							MarkdownDescription: "A message describing the changes in this version of the template. Messages longer than 72 characters will be truncated.",
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString(""),
						},
						"directory": schema.StringAttribute{
							MarkdownDescription: "A path to the directory to create the template version from. Changes in the directory contents will trigger the creation of a new template version.",
							Required:            true,
						},
						"directory_hash": schema.StringAttribute{
							Computed: true,
						},
						"active": schema.BoolAttribute{
							MarkdownDescription: "Whether this version is the active version of the template. Only one version can be active at a time.",
							Computed:            true,
							Optional:            true,
							Default:             booldefault.StaticBool(false),
						},
						"tf_vars": schema.SetNestedAttribute{
							MarkdownDescription: "Terraform variables for the template version.",
							Optional:            true,
							NestedObject:        variableNestedObject,
						},
						"provisioner_tags": schema.SetNestedAttribute{
							MarkdownDescription: "Provisioner tags for the template version.",
							Optional:            true,
							NestedObject:        variableNestedObject,
						},
						"revision_num": schema.Int64Attribute{
							MarkdownDescription: "The ordinal appended to the name_prefix to generate a unique name for the template version.",
							Computed:            true,
						},
						"full_name": schema.StringAttribute{
							MarkdownDescription: "The full name of the template version, as on the Coder deployment.",
							Computed:            true,
						},
					},
					PlanModifiers: []planmodifier.Object{
						NewDirectoryHashPlanModifier(),
						objectplanmodifier.UseStateForUnknown(),
					},
				},
			},
		},
	}
}

func (r *TemplateResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TemplateResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.OrganizationID.IsUnknown() {
		data.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	if data.DisplayName.IsUnknown() {
		data.DisplayName = data.Name
	}

	client := r.data.Client
	orgID := data.OrganizationID.ValueUUID()
	var templateResp codersdk.Template
	for idx, version := range data.Versions {
		newVersionRequest := newVersionRequest{
			Version:        &version,
			OrganizationID: orgID,
			RevisionNum:    0,
		}
		if idx > 0 {
			newVersionRequest.TemplateID = &templateResp.ID
		}
		versionResp, err := newVersion(ctx, client, newVersionRequest)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", err.Error())
			return
		}
		if idx == 0 {
			tflog.Trace(ctx, "creating template")
			templateResp, err = client.CreateTemplate(ctx, orgID, codersdk.CreateTemplateRequest{
				Name:                       data.Name.ValueString(),
				DisplayName:                data.DisplayName.ValueString(),
				Description:                data.Description.ValueString(),
				VersionID:                  versionResp.ID,
				AllowUserAutostart:         data.AllowUserAutoStart.ValueBoolPointer(),
				AllowUserAutostop:          data.AllowUserAutoStop.ValueBoolPointer(),
				Icon:                       data.Icon.ValueString(),
				DisableEveryoneGroupAccess: true,
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create template: %s", err))
				return
			}
			tflog.Trace(ctx, "successfully created template", map[string]any{
				"id": templateResp.ID,
			})

			if !data.ACL.IsNull() {
				tflog.Trace(ctx, "updating template ACL")
				var acl ACL
				resp.Diagnostics.Append(
					data.ACL.As(ctx, &acl, basetypes.ObjectAsOptions{})...,
				)
				if resp.Diagnostics.HasError() {
					return
				}
				err = client.UpdateTemplateACL(ctx, templateResp.ID, convertACLToRequest(acl))
				if err != nil {
					resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create template ACL: %s", err))
					return
				}
				tflog.Trace(ctx, "successfully updated template ACL")
			}
		}
		if version.Active.ValueBool() {
			tflog.Trace(ctx, "marking template version as active", map[string]any{
				"version_id":  versionResp.ID,
				"template_id": templateResp.ID,
			})
			err := client.UpdateActiveTemplateVersion(ctx, templateResp.ID, codersdk.UpdateActiveTemplateVersion{
				ID: versionResp.ID,
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to set active template version: %s", err))
				return
			}
			tflog.Trace(ctx, "marked template version as active")
		}
		data.Versions[idx].FullName = types.StringValue(versionResp.Name)
		data.Versions[idx].ID = UUIDValue(versionResp.ID)
		data.Versions[idx].RevisionNum = types.Int64Value(newVersionRequest.RevisionNum)
	}
	data.ID = UUIDValue(templateResp.ID)
	data.DisplayName = types.StringValue(templateResp.DisplayName)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TemplateResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	templateID := data.ID.ValueUUID()

	template, err := client.Template(ctx, templateID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template: %s", err))
		return
	}

	data.Name = types.StringValue(template.Name)
	data.DisplayName = types.StringValue(template.DisplayName)
	data.Description = types.StringValue(template.Description)
	data.OrganizationID = UUIDValue(template.OrganizationID)
	data.Icon = types.StringValue(template.Icon)
	data.AllowUserAutoStart = types.BoolValue(template.AllowUserAutostart)
	data.AllowUserAutoStop = types.BoolValue(template.AllowUserAutostop)

	if !data.ACL.IsNull() {
		tflog.Trace(ctx, "reading template ACL")
		acl, err := client.TemplateACL(ctx, templateID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template ACL: %s", err))
			return
		}
		tfACL := convertResponseToACL(acl)
		aclObj, diag := types.ObjectValueFrom(ctx, aclTypeAttr, tfACL)
		diag.Append(diag...)
		if diag.HasError() {
			return
		}
		data.ACL = aclObj
		tflog.Trace(ctx, "read template ACL")
	}

	for idx, version := range data.Versions {
		versionID := version.ID.ValueUUID()
		versionResp, err := client.TemplateVersion(ctx, versionID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template version: %s", err))
			return
		}
		data.Versions[idx].FullName = types.StringValue(versionResp.Name)
		data.Versions[idx].Message = types.StringValue(versionResp.Message)
		active := false
		if versionResp.ID == template.ActiveVersionID {
			active = true
		}
		data.Versions[idx].Active = types.BoolValue(active)
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var planState TemplateResourceModel
	var curState TemplateResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planState)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &curState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if planState.OrganizationID.IsUnknown() {
		planState.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	if planState.DisplayName.IsUnknown() {
		planState.DisplayName = planState.Name
	}

	orgID := planState.OrganizationID.ValueUUID()

	templateID := planState.ID.ValueUUID()

	client := r.data.Client

	templateMetadataChanged := !planState.EqualTemplateMetadata(curState)
	if templateMetadataChanged {
		tflog.Trace(ctx, "change in template metadata detected, updating.")
		_, err := client.UpdateTemplateMeta(ctx, templateID, codersdk.UpdateTemplateMeta{
			Name:                       planState.Name.ValueString(),
			DisplayName:                planState.DisplayName.ValueString(),
			Description:                planState.Description.ValueString(),
			AllowUserAutostart:         planState.AllowUserAutoStart.ValueBool(),
			AllowUserAutostop:          planState.AllowUserAutoStop.ValueBool(),
			Icon:                       planState.Icon.ValueString(),
			DisableEveryoneGroupAccess: true,
		})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template metadata: %s", err))
			return
		}
		tflog.Trace(ctx, "successfully updated template metadata")
	}

	// Since the everyone group always gets deleted by `DisableEveryoneGroupAccess`, we need to run this even if there
	// were no ACL changes, in case the template metadata was updated.
	if !planState.ACL.IsNull() && (!curState.ACL.Equal(planState.ACL) || templateMetadataChanged) {
		var acl ACL
		resp.Diagnostics.Append(planState.ACL.As(ctx, &acl, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}
		err := client.UpdateTemplateACL(ctx, templateID, convertACLToRequest(acl))
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template ACL: %s", err))
			return
		}
		tflog.Trace(ctx, "successfully updated template ACL")
	}

	for idx, plannedVersion := range planState.Versions {
		var curVersionName string
		// All versions in the state are guaranteed to have known name prefixes
		foundVersion := curState.Versions.ByNamePrefix(plannedVersion.NamePrefix)
		// If the version is new (name prefix doesn't exist already), or if the directory hash has changed, create a
		// new version.
		if foundVersion == nil || !foundVersion.DirectoryHash.Equal(plannedVersion.DirectoryHash) {
			tflog.Trace(ctx, "discovered a new or modified template version")
			var curRevs int64 = 0
			if foundVersion != nil {
				curRevs = foundVersion.RevisionNum.ValueInt64() + 1
			}
			versionResp, err := newVersion(ctx, client, newVersionRequest{
				Version:        &plannedVersion,
				OrganizationID: orgID,
				TemplateID:     &templateID,
				RevisionNum:    curRevs,
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", err.Error())
				return
			}
			planState.Versions[idx].RevisionNum = types.Int64Value(curRevs)
			curVersionName = versionResp.Name
		} else {
			// Or if it's an existing version, get the full name to look it up
			planState.Versions[idx].RevisionNum = foundVersion.RevisionNum
			curVersionName = foundVersion.FullName.ValueString()
		}
		versionResp, err := client.TemplateVersionByName(ctx, templateID, curVersionName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template version: %s", err))
			return
		}

		if versionResp.Message != plannedVersion.Message.ValueString() {
			_, err := client.UpdateTemplateVersion(ctx, versionResp.ID, codersdk.PatchTemplateVersionRequest{
				Name:    versionResp.Name,
				Message: plannedVersion.Message.ValueStringPointer(),
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template version metadata: %s", err))
				return
			}
		}

		if plannedVersion.Active.ValueBool() {
			tflog.Trace(ctx, "marking template version as active", map[string]any{
				"version_id":  versionResp.ID,
				"template_id": templateID,
			})
			err := client.UpdateActiveTemplateVersion(ctx, templateID, codersdk.UpdateActiveTemplateVersion{
				ID: versionResp.ID,
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update active template version: %s", err))
				return
			}
			tflog.Trace(ctx, "marked template version as active")
		}
		planState.Versions[idx].ID = UUIDValue(versionResp.ID)
		planState.Versions[idx].FullName = types.StringValue(versionResp.Name)
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &planState)...)
}

func (r *TemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TemplateResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client

	templateID := data.ID.ValueUUID()

	tflog.Trace(ctx, "deleting template")
	err := client.DeleteTemplate(ctx, templateID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to delete template: %s", err))
		return
	}
}

func (r *TemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ConfigValidators implements resource.ResourceWithConfigValidators.
func (r *TemplateResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{}
}

type versionsValidator struct{}

func NewVersionsValidator() validator.List {
	return &versionsValidator{}
}

// Description implements validator.List.
func (a *versionsValidator) Description(ctx context.Context) string {
	return a.MarkdownDescription(ctx)
}

// MarkdownDescription implements validator.List.
func (a *versionsValidator) MarkdownDescription(context.Context) string {
	return "Validate that exactly one template version has active set to true."
}

// ValidateList implements validator.List.
func (a *versionsValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	var data []TemplateVersion
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &data, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if only one item in Version has active set to true
	active := false
	for _, version := range data {
		if version.Active.ValueBool() {
			if active {
				resp.Diagnostics.AddError("Client Error", "Only one template version can be active at a time.")
				return
			}
			active = true
		}
	}
	if !active {
		resp.Diagnostics.AddError("Client Error", "At least one template version must be active.")
	}

	// Check if all versions have unique name prefixes
	namePrefixes := make(map[string]bool)
	for _, version := range data {
		namePrefix := version.NamePrefix.ValueString()
		if _, ok := namePrefixes[namePrefix]; ok {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Template version name prefix must be unique, found duplicate: `%s`", namePrefix))
			return
		}
		namePrefixes[namePrefix] = true
	}
}

var _ validator.List = &versionsValidator{}

type directoryHashPlanModifier struct{}

// Description implements planmodifier.Object.
func (d *directoryHashPlanModifier) Description(ctx context.Context) string {
	return d.MarkdownDescription(ctx)
}

// MarkdownDescription implements planmodifier.Object.
func (d *directoryHashPlanModifier) MarkdownDescription(context.Context) string {
	return "Compute the hash of a directory."
}

// PlanModifyObject implements planmodifier.Object.
func (d *directoryHashPlanModifier) PlanModifyObject(ctx context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	attributes := req.PlanValue.Attributes()
	directory, ok := attributes["directory"].(types.String)
	if !ok {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("unexpected type for directory, got: %T", directory))
		return
	}

	hash, err := computeDirectoryHash(directory.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to compute directory hash: %s", err))
		return
	}
	attributes["directory_hash"] = types.StringValue(hash)
	out, diag := types.ObjectValue(req.PlanValue.AttributeTypes(ctx), attributes)
	if diag.HasError() {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create plan object: %s", diag))
		return
	}
	resp.PlanValue = out
}

func NewDirectoryHashPlanModifier() planmodifier.Object {
	return &directoryHashPlanModifier{}
}

var _ planmodifier.Object = &directoryHashPlanModifier{}

func uploadDirectory(ctx context.Context, client *codersdk.Client, logger slog.Logger, directory string) (*codersdk.UploadResponse, error) {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		err := provisionersdk.Tar(pipeWriter, logger, directory, provisionersdk.TemplateArchiveLimit)
		_ = pipeWriter.CloseWithError(err)
	}()
	defer pipeReader.Close()
	content := pipeReader
	resp, err := client.Upload(ctx, codersdk.ContentTypeTar, bufio.NewReader(content))
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func waitForJob(ctx context.Context, client *codersdk.Client, version *codersdk.TemplateVersion) error {
	const maxRetries = 3
	for retries := 0; retries < maxRetries; retries++ {
		logs, closer, err := client.TemplateVersionLogsAfter(ctx, version.ID, 0)
		defer closer.Close()
		if err != nil {
			return fmt.Errorf("begin streaming logs: %w", err)
		}
		for {
			logs, ok := <-logs
			if !ok {
				break
			}
			tflog.Trace(ctx, logs.Output, map[string]interface{}{
				"job_id":     logs.ID,
				"job_stage":  logs.Stage,
				"log_source": logs.Source,
				"level":      logs.Level,
				"created_at": logs.CreatedAt,
			})
		}
		latestResp, err := client.TemplateVersion(ctx, version.ID)
		if err != nil {
			return err
		}
		if latestResp.Job.Status.Active() {
			tflog.Warn(ctx, fmt.Sprintf("provisioner job still active, continuing to wait...: %s", latestResp.Job.Status))
			continue
		}
		if latestResp.Job.Status != codersdk.ProvisionerJobSucceeded {
			return fmt.Errorf("provisioner job did not succeed: %s (%s)", latestResp.Job.Status, latestResp.Job.Error)
		}
		return nil
	}
	return fmt.Errorf("provisioner job did not complete after %d retries", maxRetries)
}

type newVersionRequest struct {
	OrganizationID uuid.UUID
	Version        *TemplateVersion
	TemplateID     *uuid.UUID
	RevisionNum    int64
}

func newVersion(ctx context.Context, client *codersdk.Client, req newVersionRequest) (*codersdk.TemplateVersion, error) {
	directory := req.Version.Directory.ValueString()
	tflog.Trace(ctx, "uploading directory")
	uploadResp, err := uploadDirectory(ctx, client, slog.Make(newTFLogSink(ctx)), directory)
	if err != nil {
		return nil, fmt.Errorf("failed to upload directory: %s", err)
	}
	tflog.Trace(ctx, "successfully uploaded directory")
	// TODO(ethanndickson): Uncomment when a released `codersdk` exports template variable parsing
	// tflog.Trace(ctx,"discovering and parsing vars files")
	// varFiles, err := codersdk.DiscoverVarsFiles(directory)
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to discover vars files: %s", err)
	// }
	// vars, err := codersdk.ParseUserVariableValues(varFiles, "", []string{})
	// if err != nil {
	// 	return nil, fmt.Errorf("failed to parse user variable values: %s", err)
	// }
	// tflog.Trace(ctx,"discovered and parsed vars files", map[string]any{
	// 	"vars": vars,
	// })
	vars := make([]codersdk.VariableValue, 0, len(req.Version.TerraformVariables))
	for _, variable := range req.Version.TerraformVariables {
		vars = append(vars, codersdk.VariableValue{
			Name:  variable.Name.ValueString(),
			Value: variable.Value.ValueString(),
		})
	}
	randPart, err := cryptorand.String(6)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %s", err)
	}

	versionName := fmt.Sprintf("%d-%s", req.RevisionNum, randPart)
	if req.Version.NamePrefix.ValueString() != "" {
		versionName = fmt.Sprintf("%s-%s", req.Version.NamePrefix.ValueString(), versionName)
	}
	tmplVerReq := codersdk.CreateTemplateVersionRequest{
		Name:               versionName,
		Message:            req.Version.Message.ValueString(),
		StorageMethod:      codersdk.ProvisionerStorageMethodFile,
		Provisioner:        codersdk.ProvisionerTypeTerraform,
		FileID:             uploadResp.ID,
		UserVariableValues: vars,
	}
	if req.TemplateID != nil {
		tmplVerReq.TemplateID = *req.TemplateID
	}
	tflog.Trace(ctx, "creating template version")
	versionResp, err := client.CreateTemplateVersion(ctx, req.OrganizationID, tmplVerReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create template version: %s", err)
	}
	tflog.Trace(ctx, "waiting for template version import job.")
	err = waitForJob(ctx, client, &versionResp)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for job: %s", err)
	}
	tflog.Trace(ctx, "successfully created template version")
	return &versionResp, nil
}

func convertACLToRequest(permissions ACL) codersdk.UpdateTemplateACL {
	var userPerms = make(map[string]codersdk.TemplateRole)
	for _, perm := range permissions.UserPermissions {
		userPerms[perm.ID.ValueString()] = codersdk.TemplateRole(perm.Role.ValueString())
	}
	var groupPerms = make(map[string]codersdk.TemplateRole)
	for _, perm := range permissions.GroupPermissions {
		groupPerms[perm.ID.ValueString()] = codersdk.TemplateRole(perm.Role.ValueString())
	}
	return codersdk.UpdateTemplateACL{
		UserPerms:  userPerms,
		GroupPerms: groupPerms,
	}
}

func convertResponseToACL(acl codersdk.TemplateACL) ACL {
	userPerms := make([]Permission, 0, len(acl.Users))
	for _, user := range acl.Users {
		userPerms = append(userPerms, Permission{
			ID:   types.StringValue(user.ID.String()),
			Role: types.StringValue(string(user.Role)),
		})
	}
	groupPerms := make([]Permission, 0, len(acl.Groups))
	for _, group := range acl.Groups {
		groupPerms = append(groupPerms, Permission{
			ID:   types.StringValue(group.ID.String()),
			Role: types.StringValue(string(group.Role)),
		})
	}
	return ACL{
		UserPermissions:  userPerms,
		GroupPermissions: groupPerms,
	}
}
