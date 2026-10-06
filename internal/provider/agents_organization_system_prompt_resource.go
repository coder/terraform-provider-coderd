package provider

import (
	"context"
	"fmt"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &AgentsOrganizationSystemPromptResource{}
var _ resource.ResourceWithImportState = &AgentsOrganizationSystemPromptResource{}
var _ resource.ResourceWithModifyPlan = &AgentsOrganizationSystemPromptResource{}

// First release serving organization chat system prompts (coder/coder#30272).
const agentsOrganizationSystemPromptMinVersion = "2.39.0"

type AgentsOrganizationSystemPromptResource struct {
	*CoderdProviderData
}

type AgentsOrganizationSystemPromptResourceModel struct {
	OrganizationID UUID                        `tfsdk:"organization_id"`
	SystemPrompt   agentsSystemPromptTextValue `tfsdk:"system_prompt"`
}

func NewAgentsOrganizationSystemPromptResource() resource.Resource {
	return &AgentsOrganizationSystemPromptResource{}
}

func (r *AgentsOrganizationSystemPromptResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents_organization_system_prompt"
}

func (r *AgentsOrganizationSystemPromptResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `~> This resource is experimental. Changes are to be expected, and we recommend using it with caution in production environments.

The organization instructions for Coder Agents in one organization (` + "`Admin settings → AI → Coder Agents → Organization settings`" + ` in the dashboard). When Coder creates a chat in the organization, it adds them after the deployment system prompt managed by ` + "`coderd_agents_system_prompt`" + `; they never replace it. A change applies only to chats created afterward.

Declare at most one of these resources per organization; duplicate resources for the same organization silently overwrite each other.

Coder sanitizes the stored prompt (strips invisible Unicode characters, normalizes line endings, collapses runs of blank lines, and trims surrounding whitespace), and this resource compares values the same way, so a trailing newline from ` + "`file(...)`" + ` does not cause drift after apply. On the first plan after an import, a configured value that differs from the live one only by sanitization shows a single in-place normalization update and then converges; use ` + "`trimspace(file(...))`" + ` to avoid even that.

~> **Warning**
If the organization instructions were configured out of band, ` + "`terraform import`" + ` this resource before the first apply. Otherwise Terraform overwrites the live value; a plan-time warning is emitted when this is about to happen.

~> **Warning**
` + "`terraform destroy`" + ` clears the organization instructions. The API has no delete operation for this setting.

~> **Warning**
This resource requires Coder version [` + agentsOrganizationSystemPromptMinVersion + `](https://github.com/coder/coder/releases/tag/v` + agentsOrganizationSystemPromptMinVersion + `) or later, and a token for a user who can edit the organization's Coder Agents models, such as an organization admin.
`,
		Attributes: map[string]schema.Attribute{
			"organization_id": schema.StringAttribute{
				CustomType:          UUIDType,
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The ID of the organization. Defaults to the provider default organization ID. Changing it clears the instructions of the previous organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplaceIfConfigured(),
				},
			},
			"system_prompt": schema.StringAttribute{
				CustomType: agentsSystemPromptTextType{},
				Required:   true,
				MarkdownDescription: "The organization instructions, typically `file(\"${path.module}/organization-instructions.md\")`. " +
					"Coder rejects values larger than the deployment's `CODER_CHAT_MAX_PROMPT_BYTES` limit (128 KiB by default) after sanitization.",
			},
		},
	}
}

func (r *AgentsOrganizationSystemPromptResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AgentsOrganizationSystemPromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AgentsOrganizationSystemPromptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID := data.OrganizationID.ValueUUID()
	prompt, err := r.Client.OrganizationChatSystemPrompt(ctx, orgID)
	if err != nil {
		resp.Diagnostics.Append(agentsOrganizationSystemPromptDiag("read", orgID, err)...)
		return
	}

	data.SystemPrompt = newAgentsSystemPromptTextValue(prompt.SystemPrompt)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentsOrganizationSystemPromptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AgentsOrganizationSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.OrganizationID.IsNull() || data.OrganizationID.IsUnknown() {
		data.OrganizationID = UUIDValue(r.DefaultOrganizationID)
	}

	tflog.Trace(ctx, "creating organization chat system prompt", map[string]any{
		"organization_id": data.OrganizationID.ValueString(),
	})

	resp.Diagnostics.Append(r.put(ctx, "create", data.OrganizationID.ValueUUID(), data.SystemPrompt.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentsOrganizationSystemPromptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data AgentsOrganizationSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "updating organization chat system prompt", map[string]any{
		"organization_id": data.OrganizationID.ValueString(),
	})

	resp.Diagnostics.Append(r.put(ctx, "update", data.OrganizationID.ValueUUID(), data.SystemPrompt.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentsOrganizationSystemPromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AgentsOrganizationSystemPromptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "clearing organization chat system prompt", map[string]any{
		"organization_id": data.OrganizationID.ValueString(),
	})

	// The API has no DELETE; an empty prompt clears the setting.
	resp.Diagnostics.Append(r.put(ctx, "clear", data.OrganizationID.ValueUUID(), "")...)
}

func (r *AgentsOrganizationSystemPromptResource) put(ctx context.Context, action string, orgID uuid.UUID, prompt string) diag.Diagnostics {
	err := r.Client.UpdateOrganizationChatSystemPrompt(ctx, orgID, codersdk.UpdateOrganizationChatSystemPromptRequest{
		SystemPrompt: prompt,
	})
	if err != nil {
		return agentsOrganizationSystemPromptDiag(action, orgID, err)
	}
	return nil
}

func (r *AgentsOrganizationSystemPromptResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_agents_organization_system_prompt is experimental. Changes are expected, and it is not recommended for production use.",
	)

	if req.Plan.Raw.IsNull() {
		return
	}
	// Import populates state without running Create, so only warn on true creates.
	if !req.State.Raw.IsNull() {
		return
	}
	if r.CoderdProviderData == nil {
		return
	}

	var plan, config AgentsOrganizationSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Configured values can still be unknown during planning.
	if plan.SystemPrompt.IsUnknown() || plan.SystemPrompt.IsNull() || config.OrganizationID.IsUnknown() {
		return
	}
	orgID := r.DefaultOrganizationID
	if !config.OrganizationID.IsNull() {
		orgID = config.OrganizationID.ValueUUID()
	}

	live, err := r.Client.OrganizationChatSystemPrompt(ctx, orgID)
	if err != nil {
		// This lookup is advisory; CRUD reports endpoint failures.
		tflog.Debug(ctx, "skipping organization chat system prompt plan-time check", map[string]any{
			"organization_id": orgID.String(),
			"error":           err.Error(),
		})
		return
	}

	if live.SystemPrompt != "" &&
		codersdk.SanitizePromptText(live.SystemPrompt) != codersdk.SanitizePromptText(plan.SystemPrompt.ValueString()) {
		resp.Diagnostics.AddAttributeWarning(
			pathSystemPrompt,
			"Overwriting an out-of-band value",
			fmt.Sprintf("Organization %s already has organization instructions configured, and applying will overwrite them. "+
				"Terraform has no prior state for this resource, so this change is not shown as a diff.\n\n"+
				"If you meant to adopt the organization's existing value rather than overwrite it, run "+
				"`terraform import coderd_agents_organization_system_prompt.<name> %s` first.", orgID, orgID),
		)
	}
}

func (r *AgentsOrganizationSystemPromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The endpoint accepts an organization name or UUID.
	org, err := r.Client.OrganizationByName(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get organization %q, got error: %s", req.ID, err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), org.ID.String())...)
}

// Coder soft-deletes organizations and keeps serving this endpoint for them,
// so a 404 indicates an older Coder version or missing permissions rather than
// a deleted setting.
func agentsOrganizationSystemPromptDiag(action string, orgID uuid.UUID, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	if isNotFound(err) {
		diags.AddError(
			"Organization System Prompt Endpoint Unavailable",
			fmt.Sprintf("Unable to %s the system prompt of organization %s: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later and a token for a user who can edit the organization's "+
				"Coder Agents models; upgrade the deployment or use a token with the required permissions. If neither is possible, "+
				"remove `coderd_agents_organization_system_prompt` from your configuration. Original error: %s",
				action, orgID, fmt.Sprintf("/api/v2/organizations/%s/chats/config/system-prompt", orgID),
				agentsOrganizationSystemPromptMinVersion, err),
		)
		return diags
	}

	diags.AddError("Client Error", fmt.Sprintf("unable to %s the system prompt of organization %s, got error: %s", action, orgID, err))
	return diags
}
