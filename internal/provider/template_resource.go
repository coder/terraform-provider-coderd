package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"cdr.dev/slog"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
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

	Name                           types.String `tfsdk:"name"`
	DisplayName                    types.String `tfsdk:"display_name"`
	Description                    types.String `tfsdk:"description"`
	OrganizationID                 UUID         `tfsdk:"organization_id"`
	Icon                           types.String `tfsdk:"icon"`
	DefaultTTLMillis               types.Int64  `tfsdk:"default_ttl_ms"`
	ActivityBumpMillis             types.Int64  `tfsdk:"activity_bump_ms"`
	AutostopRequirement            types.Object `tfsdk:"auto_stop_requirement"`
	AutostartPermittedDaysOfWeek   types.Set    `tfsdk:"auto_start_permitted_days_of_week"`
	AllowUserCancelWorkspaceJobs   types.Bool   `tfsdk:"allow_user_cancel_workspace_jobs"`
	AllowUserAutostart             types.Bool   `tfsdk:"allow_user_auto_start"`
	AllowUserAutostop              types.Bool   `tfsdk:"allow_user_auto_stop"`
	FailureTTLMillis               types.Int64  `tfsdk:"failure_ttl_ms"`
	TimeTilDormantMillis           types.Int64  `tfsdk:"time_til_dormant_ms"`
	TimeTilDormantAutoDeleteMillis types.Int64  `tfsdk:"time_til_dormant_autodelete_ms"`
	RequireActiveVersion           types.Bool   `tfsdk:"require_active_version"`
	DeprecationMessage             types.String `tfsdk:"deprecation_message"`

	// If null, we are not managing ACL via Terraform (such as for AGPL).
	ACL      types.Object `tfsdk:"acl"`
	Versions Versions     `tfsdk:"versions"`
}

// EqualTemplateMetadata returns true if two templates have identical metadata (excluding ACL).
func (m *TemplateResourceModel) EqualTemplateMetadata(other *TemplateResourceModel) bool {
	return m.Name.Equal(other.Name) &&
		m.DisplayName.Equal(other.DisplayName) &&
		m.Description.Equal(other.Description) &&
		m.OrganizationID.Equal(other.OrganizationID) &&
		m.Icon.Equal(other.Icon) &&
		m.DefaultTTLMillis.Equal(other.DefaultTTLMillis) &&
		m.ActivityBumpMillis.Equal(other.ActivityBumpMillis) &&
		m.AutostopRequirement.Equal(other.AutostopRequirement) &&
		m.AutostartPermittedDaysOfWeek.Equal(other.AutostartPermittedDaysOfWeek) &&
		m.AllowUserCancelWorkspaceJobs.Equal(other.AllowUserCancelWorkspaceJobs) &&
		m.AllowUserAutostart.Equal(other.AllowUserAutostart) &&
		m.AllowUserAutostop.Equal(other.AllowUserAutostop) &&
		m.FailureTTLMillis.Equal(other.FailureTTLMillis) &&
		m.TimeTilDormantMillis.Equal(other.TimeTilDormantMillis) &&
		m.TimeTilDormantAutoDeleteMillis.Equal(other.TimeTilDormantAutoDeleteMillis) &&
		m.RequireActiveVersion.Equal(other.RequireActiveVersion)
}

func (m *TemplateResourceModel) CheckEntitlements(ctx context.Context, features map[codersdk.FeatureName]codersdk.Feature) (diags diag.Diagnostics) {
	var autoStop AutostopRequirement
	diags.Append(m.AutostopRequirement.As(ctx, &autoStop, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}
	requiresScheduling := len(autoStop.DaysOfWeek) > 0 ||
		!m.AllowUserAutostart.ValueBool() ||
		!m.AllowUserAutostop.ValueBool() ||
		m.FailureTTLMillis.ValueInt64() != 0 ||
		m.TimeTilDormantAutoDeleteMillis.ValueInt64() != 0 ||
		m.TimeTilDormantMillis.ValueInt64() != 0 ||
		len(m.AutostartPermittedDaysOfWeek.Elements()) != 7
	requiresActiveVersion := m.RequireActiveVersion.ValueBool()
	requiresACL := !m.ACL.IsNull()
	if requiresScheduling || requiresActiveVersion || requiresACL {
		if requiresScheduling && !features[codersdk.FeatureAdvancedTemplateScheduling].Enabled {
			diags.AddError(
				"Feature not enabled",
				"Your license is not entitled to use advanced template scheduling, so you cannot modify any of the following fields from their defaults: auto_stop_requirement, auto_start_permitted_days_of_week, allow_user_auto_start, allow_user_auto_stop, failure_ttl_ms, time_til_dormant_ms, time_til_dormant_autodelete_ms.",
			)
			return
		}
		if requiresActiveVersion && !features[codersdk.FeatureAccessControl].Enabled {
			diags.AddError(
				"Feature not enabled",
				"Your license is not entitled to use access control, so you cannot set require_active_version.",
			)
			return
		}
		if requiresACL && !features[codersdk.FeatureTemplateRBAC].Enabled {
			diags.AddError(
				"Feature not enabled",
				"Your license is not entitled to use template access control, so you cannot set acl.",
			)
			return
		}
	}
	return
}

type TemplateVersion struct {
	ID                 UUID         `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Message            types.String `tfsdk:"message"`
	Directory          types.String `tfsdk:"directory"`
	DirectoryHash      types.String `tfsdk:"directory_hash"`
	Active             types.Bool   `tfsdk:"active"`
	TerraformVariables []Variable   `tfsdk:"tf_vars"`
	ProvisionerTags    []Variable   `tfsdk:"provisioner_tags"`
}

type Versions []TemplateVersion

func (v Versions) ByID(id UUID) *TemplateVersion {
	for _, m := range v {
		if m.ID.Equal(id) {
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

type AutostopRequirement struct {
	DaysOfWeek []string `tfsdk:"days_of_week"`
	Weeks      int64    `tfsdk:"weeks"`
}

var autostopRequirementTypeAttr = map[string]attr.Type{
	"days_of_week": basetypes.SetType{ElemType: basetypes.StringType{}},
	"weeks":        basetypes.Int64Type{},
}

func (r *TemplateResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_template"
}

func (r *TemplateResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Coder template.\n\nLogs from building template versions are streamed from the provisioner " +
			"when the `TF_LOG` environment variable is `INFO` or higher.\n\n" +
			"When importing, the ID supplied can be either a template UUID retrieved via the API or `<organization-name>/<template-name>`.",

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
					stringvalidator.RegexMatches(nameValidRegex, "Template names must be alphanumeric with hyphens."),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "The display name of the template. Defaults to the template name.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 64),
					stringvalidator.RegexMatches(displayNameRegex, "Template display names must be alphanumeric with spaces."),
				},
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
			"default_ttl_ms": schema.Int64Attribute{
				MarkdownDescription: "The default time-to-live for all workspaces created from this template, in milliseconds.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
			},
			"activity_bump_ms": schema.Int64Attribute{
				MarkdownDescription: "The activity bump duration for all workspaces created from this template, in milliseconds. Defaults to one hour.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(3600000),
			},
			"auto_stop_requirement": schema.SingleNestedAttribute{
				MarkdownDescription: "(Enterprise) The auto-stop requirement for all workspaces created from this template.",
				Optional:            true,
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"days_of_week": schema.SetAttribute{
						MarkdownDescription: "List of days of the week on which restarts are required. Restarts happen within the user's quiet hours (in their configured timezone). If no days are specified, restarts are not required.",
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						Validators:          []validator.Set{weekValidator},
						Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
					},
					"weeks": schema.Int64Attribute{
						MarkdownDescription: "Weeks is the number of weeks between required restarts. Weeks are synced across all workspaces (and Coder deployments) using modulo math on a hardcoded epoch week of January 2nd, 2023 (the first Monday of 2023). Values of 0 or 1 indicate weekly restarts. Values of 2 indicate fortnightly restarts, etc.",
						Optional:            true,
						Computed:            true,
						Default:             int64default.StaticInt64(1),
					},
				},
				Default: objectdefault.StaticValue(types.ObjectValueMust(autostopRequirementTypeAttr, map[string]attr.Value{
					"days_of_week": types.SetValueMust(types.StringType, []attr.Value{}),
					"weeks":        types.Int64Value(1),
				})),
			},
			"auto_start_permitted_days_of_week": schema.SetAttribute{
				MarkdownDescription: "(Enterprise) List of days of the week in which autostart is allowed to happen, for all workspaces created from this template. Defaults to all days. If no days are specified, autostart is not allowed.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Validators:          []validator.Set{weekValidator},
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("monday"), types.StringValue("tuesday"), types.StringValue("wednesday"), types.StringValue("thursday"), types.StringValue("friday"), types.StringValue("saturday"), types.StringValue("sunday")})),
			},
			"allow_user_cancel_workspace_jobs": schema.BoolAttribute{
				MarkdownDescription: "Whether users can cancel in-progress workspace jobs using this template. Defaults to true.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"allow_user_auto_start": schema.BoolAttribute{
				MarkdownDescription: "(Enterprise) Whether users can auto-start workspaces created from this template. Defaults to true.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"allow_user_auto_stop": schema.BoolAttribute{
				MarkdownDescription: "(Enterprise) Whether users can auto-start workspaces created from this template. Defaults to true.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
			},
			"failure_ttl_ms": schema.Int64Attribute{
				MarkdownDescription: "(Enterprise) The max lifetime before Coder stops all resources for failed workspaces created from this template, in milliseconds.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
			},
			"time_til_dormant_ms": schema.Int64Attribute{
				MarkdownDescription: "(Enterprise) The max lifetime before Coder locks inactive workspaces created from this template, in milliseconds.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
			},
			"time_til_dormant_autodelete_ms": schema.Int64Attribute{
				MarkdownDescription: "(Enterprise) The max lifetime before Coder permanently deletes dormant workspaces created from this template.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(0),
			},
			"require_active_version": schema.BoolAttribute{
				MarkdownDescription: "(Enterprise) Whether workspaces must be created from the active version of this template. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"deprecation_message": schema.StringAttribute{
				MarkdownDescription: "If set, the template will be marked as deprecated with the provided message and users will be blocked from creating new workspaces from it. Does nothing if set when the resource is created.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"acl": schema.SingleNestedAttribute{
				MarkdownDescription: "(Enterprise) Access control list for the template. If null, ACL policies will not be added, removed, or read by Terraform.",
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
					NewActiveVersionValidator(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							CustomType: UUIDType,
							Computed:   true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "The name of the template version. Automatically generated if not provided. If provided, the name *must* change each time the directory contents are updated.",
							Optional:            true,
							Computed:            true,
							Validators: []validator.String{
								stringvalidator.LengthBetween(1, 64),
								stringvalidator.RegexMatches(templateVersionNameRegex, "Template version names must be alphanumeric with underscores and dots."),
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
					},
				},
				PlanModifiers: []planmodifier.List{
					NewVersionsPlanModifier(),
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

	resp.Diagnostics.Append(data.CheckEntitlements(ctx, r.data.Features)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client := r.data.Client
	orgID := data.OrganizationID.ValueUUID()
	var templateResp codersdk.Template
	for idx, version := range data.Versions {
		newVersionRequest := newVersionRequest{
			Version:        &version,
			OrganizationID: orgID,
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
			tflog.Info(ctx, "creating template")
			createReq := data.toCreateRequest(ctx, resp, versionResp.ID)
			if resp.Diagnostics.HasError() {
				return
			}
			templateResp, err = client.CreateTemplate(ctx, orgID, *createReq)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create template: %s", err))
				return
			}
			tflog.Info(ctx, "successfully created template", map[string]any{
				"id": templateResp.ID,
			})

			// Read the response into the state to set computed fields
			diag := data.readResponse(ctx, &templateResp)
			if diag.HasError() {
				resp.Diagnostics.Append(diag...)
				return
			}

			if !data.ACL.IsNull() {
				tflog.Info(ctx, "updating template ACL")
				var acl ACL
				resp.Diagnostics.Append(
					data.ACL.As(ctx, &acl, basetypes.ObjectAsOptions{})...,
				)
				if resp.Diagnostics.HasError() {
					return
				}
				err = client.UpdateTemplateACL(ctx, templateResp.ID, convertACLToRequest(codersdk.TemplateACL{}, acl))
				if err != nil {
					resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to create template ACL: %s", err))
					return
				}
				tflog.Info(ctx, "successfully updated template ACL")
			}
		}
		if version.Active.ValueBool() {
			err := markActive(ctx, client, templateResp.ID, versionResp.ID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", err.Error())
				return
			}
		}
		data.Versions[idx].ID = UUIDValue(versionResp.ID)
		data.Versions[idx].Name = types.StringValue(versionResp.Name)
	}
	data.ID = UUIDValue(templateResp.ID)
	data.DisplayName = types.StringValue(templateResp.DisplayName)

	resp.Diagnostics.Append(data.Versions.setPrivateState(ctx, resp.Private)...)
	if resp.Diagnostics.HasError() {
		return
	}

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

	diag := data.readResponse(ctx, &template)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	if !data.ACL.IsNull() {
		tflog.Info(ctx, "reading template ACL")
		acl, err := client.TemplateACL(ctx, templateID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template ACL: %s", err))
			return
		}
		tfACL := convertResponseToACL(acl)
		aclObj, diag := types.ObjectValueFrom(ctx, aclTypeAttr, tfACL)
		if diag.HasError() {
			resp.Diagnostics.Append(diag...)
			return
		}
		data.ACL = aclObj
		tflog.Info(ctx, "read template ACL")
	}

	for idx, version := range data.Versions {
		versionID := version.ID.ValueUUID()
		versionResp, err := client.TemplateVersion(ctx, versionID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template version: %s", err))
			return
		}
		data.Versions[idx].Name = types.StringValue(versionResp.Name)
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
	var newState TemplateResourceModel
	var curState TemplateResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &newState)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &curState)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if newState.OrganizationID.IsUnknown() {
		newState.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	if newState.DisplayName.IsUnknown() {
		newState.DisplayName = newState.Name
	}

	resp.Diagnostics.Append(newState.CheckEntitlements(ctx, r.data.Features)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := newState.OrganizationID.ValueUUID()

	templateID := newState.ID.ValueUUID()

	client := r.data.Client

	templateMetadataChanged := !newState.EqualTemplateMetadata(&curState)
	// This is required, as the API will reject no-diff updates.
	if templateMetadataChanged {
		tflog.Info(ctx, "change in template metadata detected, updating.")
		updateReq := newState.toUpdateRequest(ctx, resp)
		if resp.Diagnostics.HasError() {
			return
		}
		_, err := client.UpdateTemplateMeta(ctx, templateID, *updateReq)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template metadata: %s", err))
			return
		}

		tflog.Info(ctx, "successfully updated template metadata")
	}

	// Since the everyone group always gets deleted by `DisableEveryoneGroupAccess`, we need to run this even if there
	// were no ACL changes but the template metadata was updated.
	if !newState.ACL.IsNull() && (!curState.ACL.Equal(newState.ACL) || templateMetadataChanged) {
		var acl ACL
		resp.Diagnostics.Append(newState.ACL.As(ctx, &acl, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}
		curACL, err := client.TemplateACL(ctx, templateID)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template ACL: %s", err))
			return
		}

		err = client.UpdateTemplateACL(ctx, templateID, convertACLToRequest(curACL, acl))
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template ACL: %s", err))
			return
		}
		tflog.Info(ctx, "successfully updated template ACL")
	}

	for idx := range newState.Versions {
		if newState.Versions[idx].ID.IsUnknown() {
			tflog.Info(ctx, "discovered a new or modified template version")
			uploadResp, err := newVersion(ctx, client, newVersionRequest{
				Version:        &newState.Versions[idx],
				OrganizationID: orgID,
				TemplateID:     &templateID,
			})
			if err != nil {
				resp.Diagnostics.AddError("Client Error", err.Error())
				return
			}
			versionResp, err := client.TemplateVersion(ctx, uploadResp.ID)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template version: %s", err))
				return
			}
			newState.Versions[idx].ID = UUIDValue(versionResp.ID)
			newState.Versions[idx].Name = types.StringValue(versionResp.Name)
			if newState.Versions[idx].Active.ValueBool() {
				err := markActive(ctx, client, templateID, newState.Versions[idx].ID.ValueUUID())
				if err != nil {
					resp.Diagnostics.AddError("Client Error", err.Error())
					return
				}
			}
		} else {
			// Since the ID was not unknown, it must be in the current state,
			// having been retrieved from the private state,
			// but the list might be a different size.
			curVersion := curState.Versions.ByID(newState.Versions[idx].ID)
			if curVersion == nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Public/Private State Mismatch: failed to find template version with ID %s", newState.Versions[idx].ID))
				return
			}
			if !curVersion.Name.Equal(newState.Versions[idx].Name) {
				_, err := client.UpdateTemplateVersion(ctx, newState.Versions[idx].ID.ValueUUID(), codersdk.PatchTemplateVersionRequest{
					Name:    newState.Versions[idx].Name.ValueString(),
					Message: newState.Versions[idx].Message.ValueStringPointer(),
				})
				if err != nil {
					resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to update template version metadata: %s", err))
					return
				}
			}
			if newState.Versions[idx].Active.ValueBool() && !curVersion.Active.ValueBool() {
				err := markActive(ctx, client, templateID, newState.Versions[idx].ID.ValueUUID())
				if err != nil {
					resp.Diagnostics.AddError("Client Error", err.Error())
					return
				}
			}
		}
	}

	resp.Diagnostics.Append(newState.Versions.setPrivateState(ctx, resp.Private)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &newState)...)
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

	tflog.Info(ctx, "deleting template")
	err := client.DeleteTemplate(ctx, templateID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to delete template: %s", err))
		return
	}
}

func (r *TemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	idParts := strings.Split(req.ID, "/")
	if len(idParts) == 1 {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	} else if len(idParts) == 2 {
		client := r.data.Client
		org, err := client.OrganizationByName(ctx, idParts[0])
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get organization with name %s: %s", idParts[0], err))
			return
		}
		template, err := client.TemplateByName(ctx, org.ID, idParts[1])
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template with name %s: %s", idParts[1], err))
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), template.ID.String())...)
		return
	} else {
		resp.Diagnostics.AddError("Client Error", "Invalid import ID format, expected a single UUID or `<organization-name>/<template-name>`")
		return
	}
}

// ConfigValidators implements resource.ResourceWithConfigValidators.
func (r *TemplateResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{}
}

type activeVersionValidator struct{}

func NewActiveVersionValidator() validator.List {
	return &activeVersionValidator{}
}

// Description implements validator.List.
func (a *activeVersionValidator) Description(ctx context.Context) string {
	return a.MarkdownDescription(ctx)
}

// MarkdownDescription implements validator.List.
func (a *activeVersionValidator) MarkdownDescription(context.Context) string {
	return "Validate that exactly one template version has active set to true."
}

// ValidateList implements validator.List.
func (a *activeVersionValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
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

	// Check all versions have unique names
	uniqueNames := make(map[string]struct{})
	for _, version := range data {
		if version.Name.IsNull() {
			continue
		}
		if _, ok := uniqueNames[version.Name.ValueString()]; ok {
			resp.Diagnostics.AddError("Client Error", "Template version names must be unique.")
			return
		}
		uniqueNames[version.Name.ValueString()] = struct{}{}
	}
}

var _ validator.List = &activeVersionValidator{}

type versionsPlanModifier struct{}

// Description implements planmodifier.Object.
func (d *versionsPlanModifier) Description(ctx context.Context) string {
	return d.MarkdownDescription(ctx)
}

// MarkdownDescription implements planmodifier.Object.
func (d *versionsPlanModifier) MarkdownDescription(context.Context) string {
	return "Compute the hash of a directory."
}

// PlanModifyObject implements planmodifier.List.
func (d *versionsPlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	var planVersions Versions
	resp.Diagnostics.Append(req.PlanValue.ElementsAs(ctx, &planVersions, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var configVersions Versions
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &configVersions, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for i := range planVersions {
		hash, err := computeDirectoryHash(planVersions[i].Directory.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to compute directory hash: %s", err))
			return
		}
		planVersions[i].DirectoryHash = types.StringValue(hash)
	}

	var lv LastVersionsByHash
	lvBytes, diag := req.Private.GetKey(ctx, LastVersionsKey)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	// If this is the first read, init the private state value
	if lvBytes == nil {
		lv = make(LastVersionsByHash)
	} else {
		err := json.Unmarshal(lvBytes, &lv)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to unmarshal private state when reading: %s", err))
			return
		}
	}

	planVersions.reconcileVersionIDs(lv, configVersions)

	resp.PlanValue, resp.Diagnostics = types.ListValueFrom(ctx, req.PlanValue.ElementType(ctx), planVersions)
}

func NewVersionsPlanModifier() planmodifier.List {
	return &versionsPlanModifier{}
}

var _ planmodifier.List = &versionsPlanModifier{}

var weekValidator = setvalidator.ValueStringsAre(
	stringvalidator.OneOf("monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"),
)

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
			tflog.Info(ctx, logs.Output, map[string]interface{}{
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
}

func newVersion(ctx context.Context, client *codersdk.Client, req newVersionRequest) (*codersdk.TemplateVersion, error) {
	directory := req.Version.Directory.ValueString()
	tflog.Info(ctx, "uploading directory")
	uploadResp, err := uploadDirectory(ctx, client, slog.Make(newTFLogSink(ctx)), directory)
	if err != nil {
		return nil, fmt.Errorf("failed to upload directory: %s", err)
	}
	tflog.Info(ctx, "successfully uploaded directory")
	tflog.Info(ctx, "discovering and parsing vars files")
	varFiles, err := codersdk.DiscoverVarsFiles(directory)
	if err != nil {
		return nil, fmt.Errorf("failed to discover vars files: %s", err)
	}
	vars, err := codersdk.ParseUserVariableValues(varFiles, "", []string{})
	if err != nil {
		return nil, fmt.Errorf("failed to parse user variable values: %s", err)
	}
	tflog.Info(ctx, "discovered and parsed vars files", map[string]any{
		"vars": vars,
	})
	for _, variable := range req.Version.TerraformVariables {
		vars = append(vars, codersdk.VariableValue{
			Name:  variable.Name.ValueString(),
			Value: variable.Value.ValueString(),
		})
	}
	tmplVerReq := codersdk.CreateTemplateVersionRequest{
		Name:               req.Version.Name.ValueString(),
		Message:            req.Version.Message.ValueString(),
		StorageMethod:      codersdk.ProvisionerStorageMethodFile,
		Provisioner:        codersdk.ProvisionerTypeTerraform,
		FileID:             uploadResp.ID,
		UserVariableValues: vars,
	}
	if req.TemplateID != nil {
		tmplVerReq.TemplateID = *req.TemplateID
	}
	tflog.Info(ctx, "creating template version")
	versionResp, err := client.CreateTemplateVersion(ctx, req.OrganizationID, tmplVerReq)
	if err != nil {
		return nil, fmt.Errorf("failed to create template version: %s", err)
	}
	tflog.Info(ctx, "waiting for template version import job.")
	err = waitForJob(ctx, client, &versionResp)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for job: %s", err)
	}
	tflog.Info(ctx, "successfully created template version")
	return &versionResp, nil
}

func markActive(ctx context.Context, client *codersdk.Client, templateID uuid.UUID, versionID uuid.UUID) error {
	tflog.Info(ctx, "marking template version as active", map[string]any{
		"version_id":  versionID.String(),
		"template_id": templateID.String(),
	})
	err := client.UpdateActiveTemplateVersion(ctx, templateID, codersdk.UpdateActiveTemplateVersion{
		ID: versionID,
	})
	if err != nil {
		return fmt.Errorf("Failed to update active template version: %s", err)
	}
	tflog.Info(ctx, "marked template version as active")
	return nil
}

func convertACLToRequest(curACL codersdk.TemplateACL, newACL ACL) codersdk.UpdateTemplateACL {
	var userPerms = make(map[string]codersdk.TemplateRole)
	for _, perm := range newACL.UserPermissions {
		userPerms[perm.ID.ValueString()] = codersdk.TemplateRole(perm.Role.ValueString())
	}
	var groupPerms = make(map[string]codersdk.TemplateRole)
	for _, perm := range newACL.GroupPermissions {
		groupPerms[perm.ID.ValueString()] = codersdk.TemplateRole(perm.Role.ValueString())
	}
	// For each user or group to remove, we need to set their role to empty
	// string.
	for _, perm := range curACL.Users {
		if _, ok := userPerms[perm.ID.String()]; !ok {
			userPerms[perm.ID.String()] = ""
		}
	}
	for _, perm := range curACL.Groups {
		if _, ok := groupPerms[perm.ID.String()]; !ok {
			groupPerms[perm.ID.String()] = ""
		}
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

func (r *TemplateResourceModel) readResponse(ctx context.Context, template *codersdk.Template) diag.Diagnostics {
	r.Name = types.StringValue(template.Name)
	r.DisplayName = types.StringValue(template.DisplayName)
	r.Description = types.StringValue(template.Description)
	r.OrganizationID = UUIDValue(template.OrganizationID)
	r.Icon = types.StringValue(template.Icon)
	r.DefaultTTLMillis = types.Int64Value(template.DefaultTTLMillis)
	r.ActivityBumpMillis = types.Int64Value(template.ActivityBumpMillis)
	asrObj, diag := types.ObjectValueFrom(ctx, autostopRequirementTypeAttr, AutostopRequirement{
		DaysOfWeek: template.AutostopRequirement.DaysOfWeek,
		Weeks:      template.AutostopRequirement.Weeks,
	})
	if diag.HasError() {
		return diag
	}
	r.AutostopRequirement = asrObj
	autoStartDays := make([]attr.Value, 0, len(template.AutostartRequirement.DaysOfWeek))
	for _, day := range template.AutostartRequirement.DaysOfWeek {
		autoStartDays = append(autoStartDays, types.StringValue(day))
	}
	r.AutostartPermittedDaysOfWeek = types.SetValueMust(types.StringType, autoStartDays)
	r.AllowUserCancelWorkspaceJobs = types.BoolValue(template.AllowUserCancelWorkspaceJobs)
	r.AllowUserAutostart = types.BoolValue(template.AllowUserAutostart)
	r.AllowUserAutostop = types.BoolValue(template.AllowUserAutostop)
	r.FailureTTLMillis = types.Int64Value(template.FailureTTLMillis)
	r.TimeTilDormantMillis = types.Int64Value(template.TimeTilDormantMillis)
	r.TimeTilDormantAutoDeleteMillis = types.Int64Value(template.TimeTilDormantAutoDeleteMillis)
	r.RequireActiveVersion = types.BoolValue(template.RequireActiveVersion)
	r.DeprecationMessage = types.StringValue(template.DeprecationMessage)
	return nil
}

func (r *TemplateResourceModel) toUpdateRequest(ctx context.Context, resp *resource.UpdateResponse) *codersdk.UpdateTemplateMeta {
	var days []string
	resp.Diagnostics.Append(
		r.AutostartPermittedDaysOfWeek.ElementsAs(ctx, &days, false)...,
	)
	if resp.Diagnostics.HasError() {
		return nil
	}
	autoStart := &codersdk.TemplateAutostartRequirement{
		DaysOfWeek: days,
	}
	var reqs AutostopRequirement
	resp.Diagnostics.Append(
		r.AutostopRequirement.As(ctx, &reqs, basetypes.ObjectAsOptions{})...,
	)
	if resp.Diagnostics.HasError() {
		return nil
	}
	autoStop := &codersdk.TemplateAutostopRequirement{
		DaysOfWeek: reqs.DaysOfWeek,
		Weeks:      reqs.Weeks,
	}
	return &codersdk.UpdateTemplateMeta{
		Name:                           r.Name.ValueString(),
		DisplayName:                    r.DisplayName.ValueString(),
		Description:                    r.Description.ValueString(),
		Icon:                           r.Icon.ValueString(),
		DefaultTTLMillis:               r.DefaultTTLMillis.ValueInt64(),
		ActivityBumpMillis:             r.ActivityBumpMillis.ValueInt64(),
		AutostopRequirement:            autoStop,
		AutostartRequirement:           autoStart,
		AllowUserCancelWorkspaceJobs:   r.AllowUserCancelWorkspaceJobs.ValueBool(),
		AllowUserAutostart:             r.AllowUserAutostart.ValueBool(),
		AllowUserAutostop:              r.AllowUserAutostop.ValueBool(),
		FailureTTLMillis:               r.FailureTTLMillis.ValueInt64(),
		TimeTilDormantMillis:           r.TimeTilDormantMillis.ValueInt64(),
		TimeTilDormantAutoDeleteMillis: r.TimeTilDormantAutoDeleteMillis.ValueInt64(),
		RequireActiveVersion:           r.RequireActiveVersion.ValueBool(),
		DeprecationMessage:             r.DeprecationMessage.ValueStringPointer(),
		// If we're managing ACL, we want to delete the everyone group
		DisableEveryoneGroupAccess: !r.ACL.IsNull(),
	}
}

func (r *TemplateResourceModel) toCreateRequest(ctx context.Context, resp *resource.CreateResponse, versionID uuid.UUID) *codersdk.CreateTemplateRequest {
	var days []string
	resp.Diagnostics.Append(
		r.AutostartPermittedDaysOfWeek.ElementsAs(ctx, &days, false)...,
	)
	if resp.Diagnostics.HasError() {
		return nil
	}
	autoStart := &codersdk.TemplateAutostartRequirement{
		DaysOfWeek: days,
	}
	var reqs AutostopRequirement
	resp.Diagnostics.Append(
		r.AutostopRequirement.As(ctx, &reqs, basetypes.ObjectAsOptions{})...,
	)
	if resp.Diagnostics.HasError() {
		return nil
	}
	autoStop := &codersdk.TemplateAutostopRequirement{
		DaysOfWeek: reqs.DaysOfWeek,
		Weeks:      reqs.Weeks,
	}
	return &codersdk.CreateTemplateRequest{
		Name:                           r.Name.ValueString(),
		DisplayName:                    r.DisplayName.ValueString(),
		Description:                    r.Description.ValueString(),
		Icon:                           r.Icon.ValueString(),
		VersionID:                      versionID,
		DefaultTTLMillis:               r.DefaultTTLMillis.ValueInt64Pointer(),
		ActivityBumpMillis:             r.ActivityBumpMillis.ValueInt64Pointer(),
		AutostopRequirement:            autoStop,
		AutostartRequirement:           autoStart,
		AllowUserCancelWorkspaceJobs:   r.AllowUserCancelWorkspaceJobs.ValueBoolPointer(),
		AllowUserAutostart:             r.AllowUserAutostart.ValueBoolPointer(),
		AllowUserAutostop:              r.AllowUserAutostop.ValueBoolPointer(),
		FailureTTLMillis:               r.FailureTTLMillis.ValueInt64Pointer(),
		TimeTilDormantMillis:           r.TimeTilDormantMillis.ValueInt64Pointer(),
		TimeTilDormantAutoDeleteMillis: r.TimeTilDormantAutoDeleteMillis.ValueInt64Pointer(),
		RequireActiveVersion:           r.RequireActiveVersion.ValueBool(),
		DisableEveryoneGroupAccess:     !r.ACL.IsNull(),
	}
}

type LastVersionsByHash = map[string][]PreviousTemplateVersion

var LastVersionsKey = "last_versions"

type PreviousTemplateVersion struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

func (v Versions) setPrivateState(ctx context.Context, ps privateState) (diags diag.Diagnostics) {
	lv := make(LastVersionsByHash)
	for _, version := range v {
		vbh, ok := lv[version.DirectoryHash.ValueString()]
		// Store the IDs and names of all versions with the same directory hash,
		// in the order they appear
		if ok {
			lv[version.DirectoryHash.ValueString()] = append(vbh, PreviousTemplateVersion{
				ID:   version.ID.ValueUUID(),
				Name: version.Name.ValueString(),
			})
		} else {
			lv[version.DirectoryHash.ValueString()] = []PreviousTemplateVersion{
				{
					ID:   version.ID.ValueUUID(),
					Name: version.Name.ValueString(),
				},
			}
		}
	}
	lvBytes, err := json.Marshal(lv)
	if err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Failed to marshal private state: %s", err))
		return diags
	}
	return ps.SetKey(ctx, LastVersionsKey, lvBytes)
}

func (planVersions Versions) reconcileVersionIDs(lv LastVersionsByHash, configVersions Versions) {
	for i := range planVersions {
		prevList, ok := lv[planVersions[i].DirectoryHash.ValueString()]
		// If not in state, mark as known after apply since we'll create a new version.
		// Versions whose Terraform configuration has not changed will have known
		// IDs at this point, so we need to set this manually.
		if !ok {
			planVersions[i].ID = NewUUIDUnknown()
			// We might have the old randomly generated name in the plan,
			// so unless the user has set it to a new one, we need to set it to
			// unknown so that a new one is generated
			if configVersions[i].Name.IsNull() {
				planVersions[i].Name = types.StringUnknown()
			}
		} else {
			// More than one candidate, try to match by name
			for j, prev := range prevList {
				// If the name is the same, use the existing ID, and remove
				// it from the previous version candidates
				if planVersions[i].Name.ValueString() == prev.Name {
					planVersions[i].ID = UUIDValue(prev.ID)
					lv[planVersions[i].DirectoryHash.ValueString()] = append(prevList[:j], prevList[j+1:]...)
					break
				}
			}
		}
	}

	// For versions whose hash was found in the private state but couldn't be
	// matched, use the leftovers in the order they appear
	for i := range planVersions {
		prevList := lv[planVersions[i].DirectoryHash.ValueString()]
		if len(prevList) > 0 && planVersions[i].ID.IsUnknown() {
			planVersions[i].ID = UUIDValue(prevList[0].ID)
			if planVersions[i].Name.IsUnknown() {
				planVersions[i].Name = types.StringValue(prevList[0].Name)
			}
			lv[planVersions[i].DirectoryHash.ValueString()] = prevList[1:]
		}
	}
}
