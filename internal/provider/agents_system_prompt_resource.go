package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &AgentsSystemPromptResource{}
var _ resource.ResourceWithImportState = &AgentsSystemPromptResource{}
var _ resource.ResourceWithModifyPlan = &AgentsSystemPromptResource{}

// First release serving the chat API under /api/v2 (coder/coder#28496).
const agentsSystemPromptMinVersion = "2.37.0"

// Mirrors coderd/exp_chats.go.
const maxAgentsSystemPromptBytes = 131072

var (
	pathSystemPrompt               = path.Root("system_prompt")
	pathIncludeDefaultSystemPrompt = path.Root("include_default_system_prompt")
)

type AgentsSystemPromptResource struct {
	*CoderdProviderData
}

type AgentsSystemPromptResourceModel struct {
	SystemPrompt               agentsSystemPromptTextValue `tfsdk:"system_prompt"`
	IncludeDefaultSystemPrompt types.Bool                  `tfsdk:"include_default_system_prompt"`
}

func NewAgentsSystemPromptResource() resource.Resource {
	return &AgentsSystemPromptResource{}
}

func (r *AgentsSystemPromptResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents_system_prompt"
}

func (r *AgentsSystemPromptResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `~> This resource is experimental. Changes are to be expected, and we recommend using it with caution in production environments.

The deployment-wide chat system prompt for Coder Agents (` + "`Settings → Instructions`" + ` in the dashboard).

This is a deployment-wide singleton. Declare it once; duplicate resources silently overwrite each other.

Coder sanitizes the stored prompt (strips invisible Unicode characters, normalizes line endings, collapses runs of blank lines, and trims surrounding whitespace), and this resource compares values the same way, so a trailing newline from ` + "`file(...)`" + ` does not cause drift after apply. On the first plan after an import, a configured value that differs from the live one only by sanitization shows a single in-place normalization update and then converges; use ` + "`trimspace(file(...))`" + ` to avoid even that.

~> **Warning**
If a system prompt was configured out of band, ` + "`terraform import`" + ` this resource before the first apply. Otherwise Terraform overwrites the live value; a plan-time warning is emitted when this is about to happen.

~> **Warning**
` + "`terraform destroy`" + ` resets the prompt to empty and ` + "`include_default_system_prompt`" + ` to ` + "`true`" + `, the defaults of a never-configured deployment. The API has no delete operation for this setting.

~> **Warning**
This resource requires Coder version [` + agentsSystemPromptMinVersion + `](https://github.com/coder/coder/releases/tag/v` + agentsSystemPromptMinVersion + `) or later, and a token with site-wide ` + "`owner`" + ` permissions.
`,
		Attributes: map[string]schema.Attribute{
			"system_prompt": schema.StringAttribute{
				CustomType: agentsSystemPromptTextType{},
				Required:   true,
				MarkdownDescription: "The custom system prompt text, typically `file(\"${path.module}/system-prompt.md\")`. " +
					"Limited to 128 KiB after sanitization.",
				Validators: []validator.String{
					agentsSystemPromptLengthValidator{},
				},
			},
			"include_default_system_prompt": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Whether the custom prompt is appended to Coder's built-in system prompt (`true`, the default) " +
					"or replaces it entirely (`false`).",
			},
		},
	}
}

func (r *AgentsSystemPromptResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AgentsSystemPromptResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.Client)
}

func (r *AgentsSystemPromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AgentsSystemPromptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	prompt, err := r.experimentalClient().GetChatSystemPrompt(ctx)
	if err != nil {
		resp.Diagnostics.Append(agentsSystemPromptDiag("read", err)...)
		return
	}

	data.SystemPrompt = newAgentsSystemPromptTextValue(prompt.SystemPrompt)
	data.IncludeDefaultSystemPrompt = types.BoolValue(prompt.IncludeDefaultSystemPrompt)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentsSystemPromptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AgentsSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "creating chat system prompt")

	resp.Diagnostics.Append(r.put(ctx, "create", &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully created chat system prompt")

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AgentsSystemPromptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data AgentsSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "updating chat system prompt")

	resp.Diagnostics.Append(r.put(ctx, "update", &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully updated chat system prompt")

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Always send include-default because an omitted field preserves the remote value.
func (r *AgentsSystemPromptResource) put(ctx context.Context, action string, data *AgentsSystemPromptResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	includeDefault := data.IncludeDefaultSystemPrompt.ValueBool()
	err := r.experimentalClient().UpdateChatSystemPrompt(ctx, codersdk.UpdateChatSystemPromptRequest{
		SystemPrompt:               data.SystemPrompt.ValueString(),
		IncludeDefaultSystemPrompt: &includeDefault,
	})
	if err != nil {
		diags.Append(agentsSystemPromptDiag(action, err)...)
		return diags
	}

	return diags
}

func (r *AgentsSystemPromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "deleting chat system prompt")

	// The API has no DELETE, so restore the deployment defaults.
	includeDefault := true
	err := r.experimentalClient().UpdateChatSystemPrompt(ctx, codersdk.UpdateChatSystemPromptRequest{
		SystemPrompt:               "",
		IncludeDefaultSystemPrompt: &includeDefault,
	})
	if err != nil {
		resp.Diagnostics.Append(agentsSystemPromptDiag("reset", err)...)
		return
	}

	tflog.Trace(ctx, "successfully deleted chat system prompt")
}

func (r *AgentsSystemPromptResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_agents_system_prompt is experimental. Changes are expected, and it is not recommended for production use.",
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

	var data AgentsSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Required values can still be unknown during planning.
	if data.SystemPrompt.IsUnknown() || data.SystemPrompt.IsNull() {
		return
	}

	live, err := r.experimentalClient().GetChatSystemPrompt(ctx)
	if err != nil {
		// This lookup is advisory; CRUD reports endpoint failures.
		tflog.Debug(ctx, "skipping chat system prompt plan-time check", map[string]any{
			"error": err.Error(),
		})
		return
	}

	if live.SystemPrompt != "" &&
		codersdk.SanitizePromptText(live.SystemPrompt) != codersdk.SanitizePromptText(data.SystemPrompt.ValueString()) {
		resp.Diagnostics.AddAttributeWarning(
			pathSystemPrompt,
			"Overwriting an out-of-band value",
			"This deployment already has a chat system prompt configured, and applying will overwrite it. "+
				"Terraform has no prior state for this resource, so this change is not shown as a diff.\n\n"+
				"If you meant to adopt the deployment's existing value rather than overwrite it, run "+
				"`terraform import coderd_agents_system_prompt.<name> agents_system_prompt` first.",
		)
	}

	if !data.IncludeDefaultSystemPrompt.IsUnknown() && !data.IncludeDefaultSystemPrompt.IsNull() &&
		data.IncludeDefaultSystemPrompt.ValueBool() != live.IncludeDefaultSystemPrompt {
		resp.Diagnostics.AddAttributeWarning(
			pathIncludeDefaultSystemPrompt,
			"Overwriting an out-of-band value",
			fmt.Sprintf("`include_default_system_prompt` is currently `%t` on this deployment, and applying will set it "+
				"to `%t`. Terraform has no prior state for this resource, so this change is not shown as a diff.\n\n"+
				"If you meant to adopt the deployment's existing value rather than overwrite it, run "+
				"`terraform import coderd_agents_system_prompt.<name> agents_system_prompt` first.",
				live.IncludeDefaultSystemPrompt, data.IncludeDefaultSystemPrompt.ValueBool()),
		)
	}
}

func (r *AgentsSystemPromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The singleton has no ID, but Read needs a non-null placeholder state.
	resp.Diagnostics.Append(resp.State.Set(ctx, AgentsSystemPromptResourceModel{
		SystemPrompt:               newAgentsSystemPromptTextValue(""),
		IncludeDefaultSystemPrompt: types.BoolValue(true),
	})...)
}

func agentsSystemPromptDiag(action string, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		diags.AddError(
			"Chat System Prompt Endpoint Unavailable",
			fmt.Sprintf("Unable to %s the chat system prompt: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later and a token with site-wide permissions; "+
				"upgrade the deployment or use a token with the required permissions. If neither is possible, "+
				"remove `coderd_agents_system_prompt` from your configuration. Original error: %s",
				action, "/api/v2/chats/config/system-prompt", agentsSystemPromptMinVersion, err),
		)
		return diags
	}

	diags.AddError("Client Error", fmt.Sprintf("unable to %s the chat system prompt, got error: %s", action, err))
	return diags
}

type agentsSystemPromptLengthValidator struct{}

func (agentsSystemPromptLengthValidator) Description(context.Context) string {
	return fmt.Sprintf("system prompt must be at most %d bytes after sanitization", maxAgentsSystemPromptBytes)
}

func (v agentsSystemPromptLengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v agentsSystemPromptLengthValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if got := len(codersdk.SanitizePromptText(req.ConfigValue.ValueString())); got > maxAgentsSystemPromptBytes {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"System Prompt Too Long",
			fmt.Sprintf("The system prompt is %d bytes after sanitization; the maximum Coder accepts is %d bytes (128 KiB).", got, maxAgentsSystemPromptBytes),
		)
	}
}

// Compares sanitized values to absorb server-side prompt normalization.
type agentsSystemPromptTextType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = agentsSystemPromptTextType{}

func (t agentsSystemPromptTextType) String() string {
	return "agentsSystemPromptTextType"
}

func (t agentsSystemPromptTextType) Equal(o attr.Type) bool {
	if o, ok := o.(agentsSystemPromptTextType); ok {
		return t.StringType.Equal(o.StringType)
	}
	return false
}

func (t agentsSystemPromptTextType) ValueType(ctx context.Context) attr.Value {
	return agentsSystemPromptTextValue{}
}

func (t agentsSystemPromptTextType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return agentsSystemPromptTextValue{StringValue: in}, nil
}

func (t agentsSystemPromptTextType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected basetypes.StringValue", attrValue)
	}
	return agentsSystemPromptTextValue{StringValue: stringValue}, nil
}

type agentsSystemPromptTextValue struct {
	basetypes.StringValue
}

var _ basetypes.StringValuableWithSemanticEquals = agentsSystemPromptTextValue{}

func newAgentsSystemPromptTextValue(value string) agentsSystemPromptTextValue {
	return agentsSystemPromptTextValue{StringValue: basetypes.NewStringValue(value)}
}

func (v agentsSystemPromptTextValue) Type(ctx context.Context) attr.Type {
	return agentsSystemPromptTextType{}
}

func (v agentsSystemPromptTextValue) Equal(o attr.Value) bool {
	if o, ok := o.(agentsSystemPromptTextValue); ok {
		return v.StringValue.Equal(o.StringValue)
	}
	return false
}

func (v agentsSystemPromptTextValue) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(agentsSystemPromptTextValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected agentsSystemPromptTextValue, got: %T. Please report this issue to the provider developers.", newValuable),
		)
		return false, diags
	}
	return codersdk.SanitizePromptText(v.ValueString()) == codersdk.SanitizePromptText(newValue.ValueString()), diags
}
