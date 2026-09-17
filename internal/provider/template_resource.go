package provider

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"cdr.dev/slog/v3"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/coder/retry"
	"github.com/coder/terraform-provider-coderd/internal/codersdkvalidator"
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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
var (
	_ resource.Resource                     = &TemplateResource{}
	_ resource.ResourceWithImportState      = &TemplateResource{}
	_ resource.ResourceWithConfigValidators = &TemplateResource{}
)

const templateAgentsAllowedMinVersion = "2.37.0"

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
	MaxPortShareLevel              types.String `tfsdk:"max_port_share_level"`
	CORSBehavior                   types.String `tfsdk:"cors_behavior"`
	UseClassicParameterFlow        types.Bool   `tfsdk:"use_classic_parameter_flow"`
	AgentsAllowed                  types.Bool   `tfsdk:"agents_allowed"`

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
		m.RequireActiveVersion.Equal(other.RequireActiveVersion) &&
		m.DeprecationMessage.Equal(other.DeprecationMessage) &&
		m.MaxPortShareLevel.Equal(other.MaxPortShareLevel) &&
		m.CORSBehavior.Equal(other.CORSBehavior) &&
		m.UseClassicParameterFlow.Equal(other.UseClassicParameterFlow) &&
		m.AgentsAllowed.Equal(other.AgentsAllowed)
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
	requiresSharedPortsControl := m.MaxPortShareLevel.ValueString() != "" && m.MaxPortShareLevel.ValueString() != string(codersdk.WorkspaceAgentPortShareLevelPublic)
	if requiresScheduling || requiresActiveVersion || requiresACL || requiresSharedPortsControl {
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
		if requiresSharedPortsControl && !features[codersdk.FeatureControlSharedPorts].Enabled {
			diags.AddError(
				"Feature not enabled",
				"Your license is not entitled to use port sharing control, so you cannot set max_port_share_level.",
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
	Files              types.Map    `tfsdk:"files"`
	ArchiveBase64      types.String `tfsdk:"archive_base64"`
	ArchiveFile        types.String `tfsdk:"archive_file"`
	DirectoryHash      types.String `tfsdk:"directory_hash"`
	Active             types.Bool   `tfsdk:"active"`
	TerraformVariables types.Set    `tfsdk:"tf_vars"`
	ProvisionerTags    types.Set    `tfsdk:"provisioner_tags"`
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

// variableSetElemType is the attr.Type for each element in a Set of Variable.
var variableSetElemType = types.ObjectType{
	AttrTypes: map[string]attr.Type{
		"name":  types.StringType,
		"value": types.StringType,
	},
}

// variablesFromSet extracts a []Variable from a types.Set. It returns nil when
// the set is null or unknown, so callers must treat those states as "no known
// variables" rather than relying on the slice contents.
func variablesFromSet(ctx context.Context, s types.Set) ([]Variable, diag.Diagnostics) {
	if s.IsNull() || s.IsUnknown() {
		return nil, nil
	}
	var vars []Variable
	diags := s.ElementsAs(ctx, &vars, false)
	return vars, diags
}

// variablesToSet converts a []Variable into a types.Set. A nil slice becomes a
// null set so it round-trips through variablesFromSet.
func variablesToSet(ctx context.Context, vars []Variable) (types.Set, diag.Diagnostics) {
	if vars == nil {
		return types.SetNull(variableSetElemType), nil
	}
	return types.SetValueFrom(ctx, variableSetElemType, vars)
}

// emptyVariableSet returns an empty (non-null, known) types.Set with the
// correct element type.
func emptyVariableSet() types.Set {
	return types.SetValueMust(variableSetElemType, []attr.Value{})
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
				MarkdownDescription: "Role assigned to the user or group. Valid roles are `admin` and `use`.",
				Required:            true,
				Validators: []validator.String{
					templateACLRoleValidator,
				},
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
		MarkdownDescription: "A Coder template.\n\nLogs from building template versions can be optionally streamed from the provisioner " +
			"by setting the `TF_LOG` environment variable to `INFO` or higher.",
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
					codersdkvalidator.Name(),
				},
			},
			"display_name": schema.StringAttribute{
				MarkdownDescription: "The display name of the template. Defaults to the template name.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					codersdkvalidator.DisplayName(),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "A description of the template.",
				Computed:            true,
				Optional:            true,
				Default:             stringdefault.StaticString(""),
				Validators: []validator.String{
					// codersdk.CreateTemplateRequest.Description is `validate:"lt=128"`,
					// which go-playground measures in runes, not bytes.
					stringvalidator.UTF8LengthAtMost(127),
				},
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
				MarkdownDescription: "Relative path or external URL that specifies an icon to be displayed in the dashboard.",
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
				MarkdownDescription: "(Enterprise) Whether users can auto-stop workspaces created from this template. Defaults to true.",
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
			"max_port_share_level": schema.StringAttribute{
				MarkdownDescription: "(Enterprise) The maximum port share level for workspaces created from this template. Defaults to `owner` on an Enterprise deployment, or `public` otherwise.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.OneOfCaseInsensitive(string(codersdk.WorkspaceAgentPortShareLevelAuthenticated), string(codersdk.WorkspaceAgentPortShareLevelOrganization), string(codersdk.WorkspaceAgentPortShareLevelOwner), string(codersdk.WorkspaceAgentPortShareLevelPublic)),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"deprecation_message": schema.StringAttribute{
				MarkdownDescription: "If set, the template will be marked as deprecated with the provided message and users will be blocked from creating new workspaces from it. Does nothing if set when the resource is created.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"cors_behavior": schema.StringAttribute{
				MarkdownDescription: "The CORS behavior for workspace apps in this template. Valid values are `simple` (default CORS middleware) or `passthru` (bypass CORS middleware). Defaults to `simple`. Requires a Coder deployment running v2.26.0 or later.",
				Optional:            true,
				Computed:            true,
				Validators: []validator.String{
					stringvalidator.OneOfCaseInsensitive(string(codersdk.CORSBehaviorSimple), string(codersdk.CORSBehaviorPassthru)),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"use_classic_parameter_flow": schema.BoolAttribute{
				MarkdownDescription: "If true, the classic parameter flow will be used when creating workspaces from this template. Defaults to false.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"agents_allowed": schema.BoolAttribute{
				MarkdownDescription: fmt.Sprintf("Whether Coder Agents can create workspaces from this template. Coder defaults this setting to true. Requires a Coder deployment running v%s or later.", templateAgentsAllowedMinVersion),
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
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
				MarkdownDescription: "The template versions to manage. If null, Terraform will not create, update, or read template versions, and will only manage the template's other settings. At least one version (with `active = true`) is required when creating a new template, since Coder templates cannot exist without a version.",
				Optional:            true,
				Validators: []validator.List{
					listvalidator.SizeAtLeast(1),
					NewVersionsValidator(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							CustomType: UUIDType,
							Computed:   true,
						},
						"name": schema.StringAttribute{
							MarkdownDescription: "The name of the template version. Automatically generated if not provided. If provided, the name *must* change each time the version contents, or the `tf_vars` attribute are updated.",
							Optional:            true,
							Computed:            true,
							Validators: []validator.String{
								codersdkvalidator.TemplateVersionName(),
							},
						},
						"message": schema.StringAttribute{
							MarkdownDescription: "A message describing the changes in this version of the template. Messages longer than 72 characters will be truncated.",
							Optional:            true,
							Computed:            true,
							Default:             stringdefault.StaticString(""),
						},
						"directory": schema.StringAttribute{
							MarkdownDescription: "A path to the directory to create the template version from. Changes in the directory contents will trigger the creation of a new template version. Exactly one of `directory`, `files`, `archive_base64`, or `archive_file` must be set.",
							Optional:            true,
							Validators: []validator.String{
								stringvalidator.ExactlyOneOf(
									path.MatchRelative().AtParent().AtName("directory"),
									path.MatchRelative().AtParent().AtName("files"),
									path.MatchRelative().AtParent().AtName("archive_base64"),
									path.MatchRelative().AtParent().AtName("archive_file"),
								),
							},
						},
						"files": schema.MapAttribute{
							MarkdownDescription: "The contents of the template version, as a map of file path (relative to the root of the template) to file content. Paths must be relative and must not escape the root of the template, and at least one `.tf` or `.tf.json` file must be present at the root. Changes in the contents will trigger the creation of a new template version. The files are archived exactly as if they were read from a `directory`, meaning hidden files are excluded, and `terraform.tfvars`/`*.auto.tfvars` entries are read as variable values instead of being uploaded. Exactly one of `directory`, `files`, `archive_base64`, or `archive_file` must be set.",
							Optional:            true,
							ElementType:         types.StringType,
						},
						"archive_base64": schema.StringAttribute{
							MarkdownDescription: "A base64-encoded archive of the template version contents, uploaded as-is. The archive must be an uncompressed tar, a gzipped tar, or a zip archive. Changes in the archive will trigger the creation of a new template version. Unlike `directory` and `files`, variable values are not discovered from the archive, so any `*.tfvars` files it contains are ignored - use `tf_vars` instead. Exactly one of `directory`, `files`, `archive_base64`, or `archive_file` must be set.",
							Optional:            true,
						},
						"archive_file": schema.StringAttribute{
							MarkdownDescription: "A path to an archive file containing the template version contents, uploaded as-is. The archive must be an uncompressed tar, a gzipped tar, or a zip archive, such as the one produced by the `archive_file` data source. The file is read while planning, so it must already exist at plan time, and changes to its contents will trigger the creation of a new template version. Unlike `directory` and `files`, variable values are not discovered from the archive, so any `*.tfvars` files it contains are ignored - use `tf_vars` instead. Exactly one of `directory`, `files`, `archive_base64`, or `archive_file` must be set.",
							Optional:            true,
						},
						"directory_hash": schema.StringAttribute{
							MarkdownDescription: "A hash of the template version contents, used to detect when a new version needs to be created.",
							Computed:            true,
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

	// The "at least one version is required when creating" requirement is
	// enforced at plan-time by versionsPlanModifier.PlanModifyList, so it
	// doesn't need to be repeated here.

	if data.OrganizationID.IsUnknown() {
		data.OrganizationID = UUIDValue(r.data.DefaultOrganizationID)
	}

	if data.DisplayName.IsUnknown() {
		data.DisplayName = data.Name
	}

	resp.Diagnostics.Append(data.CheckEntitlements(ctx, r.data.Features())...)
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
		versionResp, logs, err := newVersion(ctx, client, newVersionRequest)
		if err != nil {
			resp.Diagnostics.AddError("Provisioner Error", formatLogs(err, logs))
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

	// TODO: Remove this update call once this provider requires a Coder
	// deployment running `v2.15.0` or later.
	if data.MaxPortShareLevel.IsUnknown() {
		data.MaxPortShareLevel = types.StringValue(string(templateResp.MaxPortShareLevel))
	} else if data.MaxPortShareLevel.ValueString() == string(templateResp.MaxPortShareLevel) {
		tflog.Info(ctx, "max port share level set to default, not updating")
	} else {
		mpslReq := data.toUpdateRequest(ctx, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		mpslResp, err := client.UpdateTemplateMeta(ctx, data.ID.ValueUUID(), *mpslReq)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to set max port share level via update: %s", err))
			return
		}
		data.MaxPortShareLevel = types.StringValue(string(mpslResp.MaxPortShareLevel))
	}

	// Set cors_behavior from the response (it's set during create via toCreateRequest)
	data.CORSBehavior = stringValueOrNull(string(templateResp.CORSBehavior))

	// TODO: Remove this update call (and the attribute) once the provider
	// requires a Coder version where this flag has been removed.
	if data.UseClassicParameterFlow.IsUnknown() {
		data.UseClassicParameterFlow = types.BoolValue(templateResp.UseClassicParameterFlow)
	} else if data.UseClassicParameterFlow.ValueBool() == templateResp.UseClassicParameterFlow {
		tflog.Info(ctx, "use classic parameter flow set to default, not updating")
	} else {
		ucpfReq := data.toUpdateRequest(ctx, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
		ucpfResp, err := client.UpdateTemplateMeta(ctx, data.ID.ValueUUID(), *ucpfReq)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to set use classic parameter flow via update: %s", err))
			return
		}
		data.UseClassicParameterFlow = types.BoolValue(ucpfResp.UseClassicParameterFlow)
	}

	// Fetch the authoritative values after the compatibility updates.
	authoritativeTemplate, err := client.Template(ctx, data.ID.ValueUUID())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template: %s", err))
		return
	}
	data.reconcileVersionedMetadata(&authoritativeTemplate)

	// Any hash left unknown at plan time (because its contents weren't known
	// yet) has to be resolved before it's written to state.
	resp.Diagnostics.Append(data.Versions.resolveContentHashes(ctx)...)
	if resp.Diagnostics.HasError() {
		return
	}

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
		if isNotFound(err) {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Template with ID %s not found. Marking as deleted.", templateID.String()))
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template: %s", err))
		return
	}

	diag := data.readResponse(ctx, &template)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	data.reconcileVersionedMetadata(&template)

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

	resp.Diagnostics.Append(newState.CheckEntitlements(ctx, r.data.Features())...)
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
		updateReq := newState.toUpdateRequest(ctx, &resp.Diagnostics)
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
			uploadResp, logs, err := newVersion(ctx, client, newVersionRequest{
				Version:        &newState.Versions[idx],
				OrganizationID: orgID,
				TemplateID:     &templateID,
			})
			if err != nil {
				resp.Diagnostics.AddError("Provisioner Error", formatLogs(err, logs))
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
	// TODO(ethanndickson): Remove this once the provider requires a Coder
	// deployment running `v2.15.0` or later.
	templateResp, err := client.Template(ctx, templateID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to get template: %s", err))
		return
	}
	newState.reconcileVersionedMetadata(&templateResp)

	resp.Diagnostics.Append(newState.Versions.resolveContentHashes(ctx)...)
	if resp.Diagnostics.HasError() {
		return
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
	return "Validate that template version names are unique and that at most one version is active."
}

// ValidateList implements validator.List.
func (a *versionsValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	var data []TemplateVersion
	resp.Diagnostics.Append(req.ConfigValue.ElementsAs(ctx, &data, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check all versions have unique names
	uniqueNames := make(map[string]struct{})
	for _, version := range data {
		if version.Name.IsNull() || version.Name.IsUnknown() {
			continue
		}
		if _, ok := uniqueNames[version.Name.ValueString()]; ok {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Template version names must be unique. `%s` appears twice.", version.Name.ValueString()))
			return
		}
		uniqueNames[version.Name.ValueString()] = struct{}{}
	}

	// Ensure at most one version is active
	active := false
	for _, version := range data {
		// `active` defaults to false, so if it's null or unknown, this is Terraform
		// requesting an early validation.
		if version.Active.IsNull() || version.Active.IsUnknown() {
			continue
		}
		if version.Active.ValueBool() {
			if active {
				resp.Diagnostics.AddError("Client Error", "Only one template version can be active at a time.")
				return
			}
			active = true
		}
	}
}

var _ validator.List = &versionsValidator{}

type versionsPlanModifier struct{}

// Description implements planmodifier.Object.
func (d *versionsPlanModifier) Description(ctx context.Context) string {
	return d.MarkdownDescription(ctx)
}

// MarkdownDescription implements planmodifier.Object.
func (d *versionsPlanModifier) MarkdownDescription(context.Context) string {
	return "Compute the hash of a template version's contents."
}

// PlanModifyObject implements planmodifier.List.
func (d *versionsPlanModifier) PlanModifyList(ctx context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	// `versions` is optional: if it's null or unknown, Terraform is not
	// managing template versions at all, so there's nothing to reconcile.
	// Returning here (without touching resp.PlanValue) leaves the planned
	// value as-is, preserving null instead of coercing it into an empty list.
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		if req.ConfigValue.IsNull() && req.State.Raw.IsNull() {
			resp.Diagnostics.AddError("Client Error",
				"At least one template version (with `active = true`) is required when "+
					"creating a new `coderd_template` resource, since Coder templates cannot "+
					"exist without a version.\nTo manage an existing template without "+
					"Terraform-managed versions, use `terraform import` instead.")
		}
		return
	}

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

	hasActiveVersion, diag := hasOneActiveVersion(configVersions)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	// req.State.Raw.IsNull() is true only when there's no prior state at all,
	// i.e. this is a genuine Create (unlike checking private state for nil,
	// this stays false for a resource that was `terraform import`ed with
	// `versions` omitted, since import populates state before this runs).
	if req.State.Raw.IsNull() && !hasActiveVersion {
		resp.Diagnostics.AddError("Client Error", "At least one template version must be active when creating a"+
			" `coderd_template` resource.\n(Subsequent resource updates can be made without an active template in the list).")
		return
	}

	for i := range planVersions {
		hash, hashDiags := versionContentHash(ctx, &planVersions[i])
		resp.Diagnostics.Append(hashDiags...)
		if resp.Diagnostics.HasError() {
			return
		}
		planVersions[i].DirectoryHash = hash
	}

	var lv LastVersionsByHash
	lvBytes, diag := req.Private.GetKey(ctx, LastVersionsKey)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}
	// If this is the first read, init the private state value.
	if lvBytes == nil {
		lv = make(LastVersionsByHash)
	} else {
		err := json.Unmarshal(lvBytes, &lv)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Failed to unmarshal private state when reading: %s", err))
			return
		}
	}

	diag = planVersions.reconcileVersionIDs(lv, configVersions, hasActiveVersion)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
		return
	}

	// Patch the original plan elements instead of rebuilding the entire list
	// from planVersions. Re-encoding the whole list with ListValueFrom() strips
	// Terraform Core's cty-level marks from nested values (for example sensitive
	// tf_vars entries), which can surface later as an inconsistent final plan
	// during deferred re-plans. Keeping the original attr.Values intact preserves
	// those marks on untouched fields.
	planElements := req.PlanValue.Elements()
	newElements := make([]attr.Value, len(planElements))

	for i, elem := range planElements {
		obj, ok := elem.(types.Object)
		if !ok {
			resp.Diagnostics.AddError("Client Error", "Expected object element in versions list")
			return
		}

		// types.Object.Attributes returns a copy of the attribute map, so it is
		// safe to mutate attrs before constructing the replacement object.
		attrs := obj.Attributes()
		attrTypes := obj.AttributeTypes(ctx)

		// Overwrite only the fields the plan modifier manages
		attrs["id"] = planVersions[i].ID
		attrs["name"] = planVersions[i].Name
		attrs["directory_hash"] = planVersions[i].DirectoryHash

		// tf_vars, provisioner_tags, directory, active, message — all untouched

		newObj, objDiag := types.ObjectValue(attrTypes, attrs)
		if objDiag.HasError() {
			resp.Diagnostics.Append(objDiag...)
			return
		}
		newElements[i] = newObj
	}

	resp.PlanValue, diag = types.ListValue(req.PlanValue.ElementType(ctx), newElements)
	if diag.HasError() {
		resp.Diagnostics.Append(diag...)
	}
}

func hasOneActiveVersion(data Versions) (hasActiveVersion bool, diags diag.Diagnostics) {
	active := false
	for _, version := range data {
		if version.Active.IsNull() || version.Active.IsUnknown() {
			// If null or unknown, the value will be defaulted to false
			continue
		}
		if version.Active.ValueBool() {
			if active {
				diags.AddError("Client Error", "Only one template version can be active at a time.")
				return
			}
			active = true
		}
	}
	return active, diags
}

func NewVersionsPlanModifier() planmodifier.List {
	return &versionsPlanModifier{}
}

var _ planmodifier.List = &versionsPlanModifier{}

var weekValidator = setvalidator.ValueStringsAre(
	stringvalidator.OneOf("monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"),
)

var templateACLRoleValidator = stringvalidator.OneOf(
	string(codersdk.TemplateRoleAdmin),
	string(codersdk.TemplateRoleUse),
)

// versionSourceKind identifies which of the mutually exclusive attributes a
// template version takes its contents from.
type versionSourceKind int

const (
	versionSourceNone versionSourceKind = iota
	versionSourceDirectory
	versionSourceFiles
	versionSourceArchive
	versionSourceArchiveFile
)

// versionSourceAttrs lists the mutually exclusive content attributes, for use
// in diagnostics.
const versionSourceAttrs = "`directory`, `files`, `archive_base64`, or `archive_file`"

// versionSource reports which content attribute is set on a version.
// `stringvalidator.ExactlyOneOf` enforces the same rule at validate time, but
// it defers whenever one of them is unknown, so the invariant is checked here
// too rather than silently picking one of the configured sources.
func versionSource(version *TemplateVersion) (versionSourceKind, diag.Diagnostics) {
	var diags diag.Diagnostics
	kind := versionSourceNone
	count := 0
	if !version.Directory.IsNull() {
		kind, count = versionSourceDirectory, count+1
	}
	if !version.Files.IsNull() {
		kind, count = versionSourceFiles, count+1
	}
	if !version.ArchiveBase64.IsNull() {
		kind, count = versionSourceArchive, count+1
	}
	if !version.ArchiveFile.IsNull() {
		kind, count = versionSourceArchiveFile, count+1
	}
	switch {
	case count == 0:
		diags.AddError("Client Error", "A template version must set exactly one of "+versionSourceAttrs+".")
	case count > 1:
		diags.AddError("Client Error", "A template version can only set one of "+versionSourceAttrs+".")
	}
	return kind, diags
}

// filesFromMap converts a version's `files` attribute into a map of relative
// path to content. It returns a nil map, without diagnostics, when the map or
// any of its elements is still unknown: the contents can neither be hashed nor
// uploaded yet, and callers treat that as "not known".
func filesFromMap(ctx context.Context, files types.Map) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	if files.IsNull() || files.IsUnknown() {
		return nil, diags
	}
	elements := files.Elements()
	for name, content := range elements {
		if content.IsUnknown() {
			return nil, diags
		}
		if content.IsNull() {
			diags.AddError("Client Error", fmt.Sprintf("The contents of file %q are null. Every entry in `files` must be a string.", name))
			return nil, diags
		}
	}
	out := make(map[string]string, len(elements))
	diags.Append(files.ElementsAs(ctx, &out, false)...)
	if diags.HasError() {
		return nil, diags
	}
	return out, diags
}

// filesHaveTerraform reports whether a `files` map contains a Terraform file at
// the root of the template. The files are archived through
// `provisionersdk.Tar`, which rejects a directory without one, but its error
// refers to the temporary directory they're written to, so this is checked up
// front instead.
func filesHaveTerraform(files map[string]string) bool {
	for name := range files {
		rel := filepath.Clean(filepath.FromSlash(name))
		if filepath.Dir(rel) != "." {
			continue
		}
		if strings.HasSuffix(rel, ".tf") || strings.HasSuffix(rel, ".tf.json") {
			return true
		}
	}
	return false
}

// writeFiles materializes a `files` map into dir so that it can be archived and
// have its variable files discovered exactly like a configured `directory`.
func writeFiles(dir string, files map[string]string) error {
	for name, content := range files {
		rel := filepath.Clean(filepath.FromSlash(name))
		if filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("invalid file path %q: paths must be relative to the root of the template, and must not escape it", name)
		}
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("failed to create directory for %q: %w", name, err)
		}
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return fmt.Errorf("failed to write %q: %w", name, err)
		}
	}
	return nil
}

// Magic bytes identifying the archive formats accepted by `archive_base64` and
// `archive_file`.
var (
	gzipMagic = []byte{0x1f, 0x8b}
	zipMagic  = []byte("PK\x03\x04")
)

// normalizeArchive sanity-checks raw archive contents and returns the bytes to
// upload along with the content type to upload them as. An uncompressed tar, a
// gzipped tar, or a zip archive is accepted: the Coder API takes tar and zip
// directly, and gzip is decompressed here so that archives produced by e.g. the
// `archive_file` data source can be used without a conversion step.
func normalizeArchive(raw []byte) (string, []byte, error) {
	switch {
	case bytes.HasPrefix(raw, gzipMagic):
		payload, err := gunzipArchive(raw)
		if err != nil {
			return "", nil, err
		}
		if err := validateTarArchive(payload); err != nil {
			return "", nil, fmt.Errorf("the gzipped archive does not contain a tar archive: %w", err)
		}
		return codersdk.ContentTypeTar, payload, nil
	case bytes.HasPrefix(raw, zipMagic):
		if err := checkArchiveSize(len(raw)); err != nil {
			return "", nil, err
		}
		if _, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw))); err != nil {
			return "", nil, fmt.Errorf("the archive is not a readable zip archive: %w", err)
		}
		return codersdk.ContentTypeZip, raw, nil
	default:
		if err := checkArchiveSize(len(raw)); err != nil {
			return "", nil, err
		}
		if err := validateTarArchive(raw); err != nil {
			return "", nil, fmt.Errorf("the archive must be an uncompressed tar, a gzipped tar, or a zip archive: %w", err)
		}
		return codersdk.ContentTypeTar, raw, nil
	}
}

func checkArchiveSize(size int) error {
	if int64(size) >= provisionersdk.TemplateArchiveLimit {
		return fmt.Errorf("archive too big. Must be < %d bytes", provisionersdk.TemplateArchiveLimit)
	}
	return nil
}

func validateTarArchive(payload []byte) error {
	_, err := tar.NewReader(bytes.NewReader(payload)).Next()
	return err
}

// gunzipArchive decompresses a gzipped archive, refusing to buffer more than
// the archive size limit so that a small, highly compressed archive can't be
// expanded into an unbounded amount of memory.
func gunzipArchive(raw []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("the archive is not a readable gzip archive: %w", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	var buf bytes.Buffer
	if _, err := io.CopyN(&buf, reader, provisionersdk.TemplateArchiveLimit); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("failed to decompress the archive: %w", err)
	}
	if err := checkArchiveSize(buf.Len()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// decodeArchive decodes and sanity-checks a version's `archive_base64`
// attribute. It's called from the plan modifier as well as at apply time, so a
// malformed archive fails the plan instead of a partially applied change.
func decodeArchive(archiveBase64 string) (string, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(archiveBase64)
	if err != nil {
		return "", nil, fmt.Errorf("`archive_base64` is not valid base64: %w", err)
	}
	return normalizeArchive(raw)
}

// readArchiveFile reads and sanity-checks the archive a version's
// `archive_file` attribute points at. Like `computeDirectoryHash`, it reads
// from disk while planning, so the archive has to exist by then, and a missing
// or malformed one fails the plan instead of a partially applied change.
func readArchiveFile(filename string) (string, []byte, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read `archive_file`: %w", err)
	}
	return normalizeArchive(raw)
}

// versionContentHash hashes a version's contents, from whichever content
// attribute is set. The hash is what decides whether a new template version
// needs to be created, so it returns an unknown string when the configured
// source isn't known yet, which plans the version as new.
func versionContentHash(ctx context.Context, version *TemplateVersion) (types.String, diag.Diagnostics) {
	source, diags := versionSource(version)
	if diags.HasError() {
		return types.StringNull(), diags
	}
	switch source {
	case versionSourceDirectory:
		if version.Directory.IsUnknown() {
			return types.StringUnknown(), diags
		}
		hash, err := computeDirectoryHash(version.Directory.ValueString())
		if err != nil {
			diags.AddError("Client Error", fmt.Sprintf("Failed to compute directory hash: %s", err))
			return types.StringNull(), diags
		}
		return types.StringValue(hash), diags
	case versionSourceFiles:
		files, fileDiags := filesFromMap(ctx, version.Files)
		diags.Append(fileDiags...)
		if diags.HasError() {
			return types.StringNull(), diags
		}
		if files == nil {
			return types.StringUnknown(), diags
		}
		if !filesHaveTerraform(files) {
			diags.AddError("Client Error", "`files` must contain at least one `.tf` or `.tf.json` file at the root of the template.")
			return types.StringNull(), diags
		}
		return types.StringValue(computeFilesHash(files)), diags
	case versionSourceArchive:
		if version.ArchiveBase64.IsUnknown() {
			return types.StringUnknown(), diags
		}
		_, archive, err := decodeArchive(version.ArchiveBase64.ValueString())
		if err != nil {
			diags.AddError("Client Error", fmt.Sprintf("Failed to read template version archive: %s", err))
			return types.StringNull(), diags
		}
		return types.StringValue(computeBytesHash(archive)), diags
	case versionSourceArchiveFile:
		if version.ArchiveFile.IsUnknown() {
			return types.StringUnknown(), diags
		}
		_, archive, err := readArchiveFile(version.ArchiveFile.ValueString())
		if err != nil {
			diags.AddError("Client Error", fmt.Sprintf("Failed to read template version archive: %s", err))
			return types.StringNull(), diags
		}
		return types.StringValue(computeBytesHash(archive)), diags
	default:
		return types.StringUnknown(), diags
	}
}

// resolveContentHashes fills in any version hash that was left unknown at plan
// time, so that the hashes written to state (and to the private state that
// matches versions across plans) are always known.
func (v Versions) resolveContentHashes(ctx context.Context) (diags diag.Diagnostics) {
	for i := range v {
		if !v[i].DirectoryHash.IsUnknown() {
			continue
		}
		hash, hashDiags := versionContentHash(ctx, &v[i])
		diags.Append(hashDiags...)
		if diags.HasError() {
			return diags
		}
		v[i].DirectoryHash = hash
	}
	return diags
}

func uploadDirectory(ctx context.Context, client *codersdk.Client, logger slog.Logger, directory string) (*codersdk.UploadResponse, error) {
	pipeReader, pipeWriter := io.Pipe()
	go func() {
		err := provisionersdk.Tar(pipeWriter, logger, directory, provisionersdk.TemplateArchiveLimit)
		_ = pipeWriter.CloseWithError(err)
	}()
	defer func() {
		if err := pipeReader.Close(); err != nil {
			tflog.Warn(ctx, "error closing template archive reader", map[string]any{
				"error": err,
			})
		}
	}()
	content := pipeReader
	resp, err := client.Upload(ctx, codersdk.ContentTypeTar, bufio.NewReader(content))
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// uploadVersionContents uploads the contents of a template version from
// whichever content attribute is set, and returns the ID of the uploaded file
// along with any variable values discovered alongside the contents.
func uploadVersionContents(ctx context.Context, client *codersdk.Client, version *TemplateVersion) (uuid.UUID, []codersdk.VariableValue, error) {
	source, diags := versionSource(version)
	if diags.HasError() {
		return uuid.Nil, nil, errors.New(diags.Errors()[0].Detail())
	}
	switch source {
	case versionSourceDirectory:
		return uploadTemplateDirectory(ctx, client, version.Directory.ValueString())
	case versionSourceFiles:
		files, fileDiags := filesFromMap(ctx, version.Files)
		if fileDiags.HasError() {
			return uuid.Nil, nil, errors.New(fileDiags.Errors()[0].Detail())
		}
		if files == nil {
			return uuid.Nil, nil, errors.New("the contents of `files` are not known")
		}
		// The files are written out and then archived like any other directory,
		// so that a version built from `files` behaves identically to the same
		// contents on disk (hidden file handling, variable file discovery, and
		// the archive size limit all included).
		dir, err := os.MkdirTemp("", "coderd-template-version-")
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("failed to create temporary directory for `files`: %s", err)
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				tflog.Warn(ctx, "error removing temporary template directory", map[string]any{
					"error": err,
				})
			}
		}()
		if err := writeFiles(dir, files); err != nil {
			return uuid.Nil, nil, fmt.Errorf("failed to write `files`: %s", err)
		}
		return uploadTemplateDirectory(ctx, client, dir)
	case versionSourceArchive:
		contentType, archive, err := decodeArchive(version.ArchiveBase64.ValueString())
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("failed to read template version archive: %s", err)
		}
		fileID, err := uploadTemplateArchive(ctx, client, contentType, archive)
		return fileID, nil, err
	case versionSourceArchiveFile:
		contentType, archive, err := readArchiveFile(version.ArchiveFile.ValueString())
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("failed to read template version archive: %s", err)
		}
		fileID, err := uploadTemplateArchive(ctx, client, contentType, archive)
		return fileID, nil, err
	default:
		return uuid.Nil, nil, errors.New("a template version must set exactly one of " + versionSourceAttrs)
	}
}

// uploadTemplateArchive uploads an already-built archive as-is, and returns the
// ID of the uploaded file. It reports no variable values: they can't be
// discovered without extracting the archive, so `tf_vars` is the only way to
// set them for the archive sources.
func uploadTemplateArchive(ctx context.Context, client *codersdk.Client, contentType string, archive []byte) (uuid.UUID, error) {
	tflog.Info(ctx, "uploading archive")
	uploadResp, err := client.Upload(ctx, contentType, bytes.NewReader(archive))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to upload archive: %s", err)
	}
	tflog.Info(ctx, "successfully uploaded archive")
	return uploadResp.ID, nil
}

// uploadTemplateDirectory archives and uploads a directory, and parses the
// variable values from any vars files it contains.
func uploadTemplateDirectory(ctx context.Context, client *codersdk.Client, directory string) (uuid.UUID, []codersdk.VariableValue, error) {
	tflog.Info(ctx, "uploading directory")
	uploadResp, err := uploadDirectory(ctx, client, slog.Make(newTFLogSink(ctx)), directory)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to upload directory: %s", err)
	}
	tflog.Info(ctx, "successfully uploaded directory")
	tflog.Info(ctx, "discovering and parsing vars files")
	varFiles, err := codersdk.DiscoverVarsFiles(directory)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to discover vars files: %s", err)
	}
	vars, err := codersdk.ParseUserVariableValues(varFiles, "", []string{})
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("failed to parse user variable values: %s", err)
	}
	tflog.Info(ctx, "discovered and parsed vars files", map[string]any{
		"vars": vars,
	})
	return uploadResp.ID, vars, nil
}

func waitForJob(ctx context.Context, client *codersdk.Client, version *codersdk.TemplateVersion) ([]codersdk.ProvisionerJobLog, error) {
	const maxRetries = 3
	var allLogs []codersdk.ProvisionerJobLog
	var lastLogID int64

	for attempts, retrier := 0, retry.New(500*time.Millisecond, 5*time.Second); attempts < maxRetries && retrier.Wait(ctx); attempts++ {
		logs, done, err := waitForJobOnce(ctx, client, version, lastLogID)
		allLogs = append(allLogs, logs...)
		if len(logs) > 0 {
			lastLogID = logs[len(logs)-1].ID
		}
		if err != nil {
			return allLogs, err
		}
		if done {
			return allLogs, nil
		}
		tflog.Warn(ctx, fmt.Sprintf("provisioner job still active, retrying (attempt %d/%d)", attempts+1, maxRetries))
	}

	if err := ctx.Err(); err != nil {
		return allLogs, err
	}
	return allLogs, fmt.Errorf("provisioner job did not complete after %d retries", maxRetries)
}

func waitForJobOnce(ctx context.Context, client *codersdk.Client, version *codersdk.TemplateVersion, after int64) ([]codersdk.ProvisionerJobLog, bool, error) {
	logCh, closer, err := client.TemplateVersionLogsAfter(ctx, version.ID, after)
	if err != nil {
		return nil, false, fmt.Errorf("begin streaming logs: %w", err)
	}
	defer func() {
		if err := closer.Close(); err != nil {
			tflog.Warn(ctx, "error closing template version log stream", map[string]any{
				"error": err,
			})
		}
	}()
	var jobLogs []codersdk.ProvisionerJobLog
	for {
		logMsg, ok := <-logCh
		if !ok {
			break
		}
		tflog.Info(ctx, logMsg.Output, map[string]interface{}{
			"job_id":     logMsg.ID,
			"job_stage":  logMsg.Stage,
			"log_source": logMsg.Source,
			"level":      logMsg.Level,
			"created_at": logMsg.CreatedAt,
		})
		if logMsg.Output != "" {
			jobLogs = append(jobLogs, logMsg)
		}
	}
	latestResp, err := client.TemplateVersion(ctx, version.ID)
	if err != nil {
		return jobLogs, false, err
	}
	if latestResp.Job.Status.Active() {
		return jobLogs, false, nil
	}
	if latestResp.Job.Status != codersdk.ProvisionerJobSucceeded {
		return jobLogs, false, fmt.Errorf("provisioner job did not succeed: %s (%s)", latestResp.Job.Status, latestResp.Job.Error)
	}
	return jobLogs, true, nil
}

type newVersionRequest struct {
	OrganizationID uuid.UUID
	Version        *TemplateVersion
	TemplateID     *uuid.UUID
}

func newVersion(ctx context.Context, client *codersdk.Client, req newVersionRequest) (*codersdk.TemplateVersion, []codersdk.ProvisionerJobLog, error) {
	var logs []codersdk.ProvisionerJobLog
	fileID, vars, err := uploadVersionContents(ctx, client, req.Version)
	if err != nil {
		return nil, logs, err
	}
	tfVars, diags := variablesFromSet(ctx, req.Version.TerraformVariables)
	if diags.HasError() {
		return nil, logs, fmt.Errorf("failed to extract terraform variables: %s", diags.Errors()[0].Detail())
	}
	for _, variable := range tfVars {
		vars = append(vars, codersdk.VariableValue{
			Name:  variable.Name.ValueString(),
			Value: variable.Value.ValueString(),
		})
	}
	provTagVars, diags := variablesFromSet(ctx, req.Version.ProvisionerTags)
	if diags.HasError() {
		return nil, logs, fmt.Errorf("failed to extract provisioner tags: %s", diags.Errors()[0].Detail())
	}
	provTags := make(map[string]string, len(provTagVars))
	for _, provisionerTag := range provTagVars {
		provTags[provisionerTag.Name.ValueString()] = provisionerTag.Value.ValueString()
	}
	tmplVerReq := codersdk.CreateTemplateVersionRequest{
		Name:               req.Version.Name.ValueString(),
		Message:            req.Version.Message.ValueString(),
		StorageMethod:      codersdk.ProvisionerStorageMethodFile,
		Provisioner:        codersdk.ProvisionerTypeTerraform,
		FileID:             fileID,
		UserVariableValues: vars,
		ProvisionerTags:    provTags,
	}
	if req.TemplateID != nil {
		tmplVerReq.TemplateID = *req.TemplateID
	}
	tflog.Info(ctx, "creating template version")
	versionResp, err := client.CreateTemplateVersion(ctx, req.OrganizationID, tmplVerReq)
	if err != nil {
		return nil, logs, fmt.Errorf("failed to create template version: %s", err)
	}
	tflog.Info(ctx, "waiting for template version import job.")
	logs, err = waitForJob(ctx, client, &versionResp)
	if err != nil {
		return nil, logs, fmt.Errorf("failed to wait for job: %s", err)
	}
	tflog.Info(ctx, "successfully created template version")
	return &versionResp, logs, nil
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
		return fmt.Errorf("failed to update active template version: %s", err)
	}
	tflog.Info(ctx, "marked template version as active")
	return nil
}

func convertACLToRequest(curACL codersdk.TemplateACL, newACL ACL) codersdk.UpdateTemplateACL {
	userPerms := make(map[string]codersdk.TemplateRole)
	for _, perm := range newACL.UserPermissions {
		userPerms[perm.ID.ValueString()] = codersdk.TemplateRole(perm.Role.ValueString())
	}
	groupPerms := make(map[string]codersdk.TemplateRole)
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

// reconcileVersionedMetadata overwrites metadata fields that older Coder
// servers can omit from mutation responses. Call it with an authoritative
// template response before writing state.
func (r *TemplateResourceModel) reconcileVersionedMetadata(template *codersdk.Template) {
	r.MaxPortShareLevel = types.StringValue(string(template.MaxPortShareLevel))
	r.CORSBehavior = stringValueOrNull(string(template.CORSBehavior))
	r.UseClassicParameterFlow = types.BoolValue(template.UseClassicParameterFlow)
	r.AgentsAllowed = types.BoolValue(template.AgentsAllowed)
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
	// TODO(ethanndickson): MaxPortShareLevel deliberately omitted, as it can't
	// be set during a create request, and we call this during `Create`.
	return nil
}

func (r *TemplateResourceModel) toUpdateRequest(ctx context.Context, diag *diag.Diagnostics) *codersdk.UpdateTemplateMeta {
	var days []string
	diag.Append(
		r.AutostartPermittedDaysOfWeek.ElementsAs(ctx, &days, false)...,
	)
	if diag.HasError() {
		return nil
	}
	autoStart := &codersdk.TemplateAutostartRequirement{
		DaysOfWeek: days,
	}
	var reqs AutostopRequirement
	diag.Append(
		r.AutostopRequirement.As(ctx, &reqs, basetypes.ObjectAsOptions{})...,
	)
	if diag.HasError() {
		return nil
	}
	autoStop := &codersdk.TemplateAutostopRequirement{
		DaysOfWeek: reqs.DaysOfWeek,
		Weeks:      reqs.Weeks,
	}
	return &codersdk.UpdateTemplateMeta{
		Name:                           r.Name.ValueStringPointer(),
		DisplayName:                    r.DisplayName.ValueStringPointer(),
		Description:                    r.Description.ValueStringPointer(),
		Icon:                           r.Icon.ValueStringPointer(),
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
		RequireActiveVersion:           r.RequireActiveVersion.ValueBoolPointer(),
		DeprecationMessage:             r.DeprecationMessage.ValueStringPointer(),
		MaxPortShareLevel:              new(codersdk.WorkspaceAgentPortShareLevel(r.MaxPortShareLevel.ValueString())),
		CORSBehavior:                   corsPtr(r.CORSBehavior),
		UseClassicParameterFlow:        boolPtrOrNil(r.UseClassicParameterFlow),
		AgentsAllowed:                  boolPtrOrNil(r.AgentsAllowed),
		// If we're managing ACL, we want to delete the everyone group.
		DisableEveryoneGroupAccess: new(!r.ACL.IsNull()),
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
		UseClassicParameterFlow:        boolPtrOrNil(r.UseClassicParameterFlow),
		AgentsAllowed:                  boolPtrOrNil(r.AgentsAllowed),
		CORSBehavior:                   corsPtr(r.CORSBehavior),
		DisableEveryoneGroupAccess:     !r.ACL.IsNull(),
	}
}

type LastVersionsByHash = map[string][]PreviousTemplateVersion

var LastVersionsKey = "last_versions"

type PreviousTemplateVersion struct {
	ID     uuid.UUID         `json:"id"`
	Name   string            `json:"name"`
	TFVars map[string]string `json:"tf_vars"`
	Active bool              `json:"active"`
}

type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

func (v Versions) setPrivateState(ctx context.Context, ps privateState) (diags diag.Diagnostics) {
	lv := make(LastVersionsByHash)
	for _, version := range v {
		vbh, ok := lv[version.DirectoryHash.ValueString()]
		versionTfVars, varDiags := variablesFromSet(ctx, version.TerraformVariables)
		if varDiags.HasError() {
			diags.Append(varDiags...)
			return diags
		}
		tfVars := make(map[string]string, len(versionTfVars))
		for _, tfVar := range versionTfVars {
			tfVars[tfVar.Name.ValueString()] = tfVar.Value.ValueString()
		}
		// Store the IDs and names of all versions with the same directory hash,
		// in the order they appear
		if ok {
			lv[version.DirectoryHash.ValueString()] = append(vbh, PreviousTemplateVersion{
				ID:     version.ID.ValueUUID(),
				Name:   version.Name.ValueString(),
				TFVars: tfVars,
				Active: version.Active.ValueBool(),
			})
		} else {
			lv[version.DirectoryHash.ValueString()] = []PreviousTemplateVersion{
				{
					ID:     version.ID.ValueUUID(),
					Name:   version.Name.ValueString(),
					TFVars: tfVars,
					Active: version.Active.ValueBool(),
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

func (planVersions Versions) reconcileVersionIDs(lv LastVersionsByHash, configVersions Versions, hasOneActiveVersion bool) (diag diag.Diagnostics) {
	// We remove versions that we've matched from `lv`, so make a copy for
	// resolving tfvar changes at the end.
	fullLv := make(LastVersionsByHash)
	for k, v := range lv {
		fullLv[k] = slices.Clone(v)
	}

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
			if configVersions[i].Name.IsNull() {
				planVersions[i].Name = types.StringValue(prevList[0].Name)
			}
			lv[planVersions[i].DirectoryHash.ValueString()] = prevList[1:]
		}
	}

	// If only the Terraform variables have changed,
	// we need to create a new version with the new variables.
	for i := range planVersions {
		if !planVersions[i].ID.IsUnknown() {
			prevs, ok := fullLv[planVersions[i].DirectoryHash.ValueString()]
			if !ok {
				continue
			}
			if tfVariablesChanged(prevs, &planVersions[i]) {
				planVersions[i].ID = NewUUIDUnknown()
				// We could always set the name to unknown here, to generate a
				// random one (this is what the Web UI currently does when
				// only updating tfvars).
				// However, I think it'd be weird if the provider just started
				// ignoring the name you set in the config, we'll instead
				// require that users update the name if they update the tfvars.
				if configVersions[i].Name.IsNull() {
					planVersions[i].Name = types.StringUnknown()
				}
			}
		}
	}

	// If a version was deactivated, and no active version was set, we need to
	// return an error to avoid a post-apply plan being non-empty.
	if !hasOneActiveVersion {
		for i := range planVersions {
			if !planVersions[i].ID.IsUnknown() {
				prevs, ok := fullLv[planVersions[i].DirectoryHash.ValueString()]
				if !ok {
					continue
				}
				if versionDeactivated(prevs, &planVersions[i]) {
					diag.AddError("Client Error", "Plan could not determine which version should be active.\n"+
						"Either specify an active version or modify the contents of the previously active version before marking it as inactive.")
					return diag
				}
			}
		}
	}
	return diag
}

func versionDeactivated(prevs []PreviousTemplateVersion, planned *TemplateVersion) bool {
	for _, prev := range prevs {
		if prev.ID == planned.ID.ValueUUID() {
			if prev.Active &&
				!planned.Active.IsNull() &&
				!planned.Active.IsUnknown() &&
				!planned.Active.ValueBool() {
				return true
			}
		}
	}
	return false
}

func tfVariablesChanged(prevs []PreviousTemplateVersion, planned *TemplateVersion) bool {
	for _, prev := range prevs {
		if prev.ID == planned.ID.ValueUUID() {
			// If the previous version has no TFVars, then it was created using
			// an older provider version.
			if prev.TFVars == nil {
				return true
			}
			// If the planned set is unknown we cannot compare values, so assume
			// the variables changed and let a new version be created.
			if planned.TerraformVariables.IsUnknown() {
				return true
			}
			plannedVars, diags := variablesFromSet(context.Background(), planned.TerraformVariables)
			// If we can't decode the planned variables, fail toward creating a
			// new version rather than silently reusing a stale one.
			if diags.HasError() {
				return true
			}
			// A subset comparison alone would miss removed variables (a planned
			// strict subset of prev passes every check), so compare counts
			// first. This also covers a null set: nil plannedVars has len 0.
			if len(plannedVars) != len(prev.TFVars) {
				return true
			}
			for _, tfVar := range plannedVars {
				if prev.TFVars[tfVar.Name.ValueString()] != tfVar.Value.ValueString() {
					return true
				}
			}
			return false
		}
	}
	return true
}

func formatLogs(err error, logs []codersdk.ProvisionerJobLog) string {
	var b strings.Builder
	b.WriteString(err.Error() + "\n")
	for _, log := range logs {
		if !log.CreatedAt.IsZero() {
			b.WriteString(log.CreatedAt.Local().Format("2006-01-02 15:04:05.000Z07:00") + " ")
		}
		b.WriteString(log.Output + "\n")
	}
	return b.String()
}
