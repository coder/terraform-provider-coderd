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

var _ resource.Resource = &ChatSystemPromptResource{}
var _ resource.ResourceWithImportState = &ChatSystemPromptResource{}
var _ resource.ResourceWithModifyPlan = &ChatSystemPromptResource{}

// First release with the chat system prompt endpoint (coder/coder#22857).
const chatSystemPromptMinVersion = "2.32.0"

// Mirrors coderd/exp_chats.go.
const maxChatSystemPromptBytes = 131072

var (
	pathSystemPrompt               = path.Root("system_prompt")
	pathIncludeDefaultSystemPrompt = path.Root("include_default_system_prompt")
)

type ChatSystemPromptResource struct {
	*CoderdProviderData
}

type ChatSystemPromptResourceModel struct {
	SystemPrompt               chatSystemPromptTextValue `tfsdk:"system_prompt"`
	IncludeDefaultSystemPrompt types.Bool                `tfsdk:"include_default_system_prompt"`
}

func NewChatSystemPromptResource() resource.Resource {
	return &ChatSystemPromptResource{}
}

func (r *ChatSystemPromptResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_chat_system_prompt"
}

func (r *ChatSystemPromptResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
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
This resource requires Coder version [` + chatSystemPromptMinVersion + `](https://github.com/coder/coder/releases/tag/v` + chatSystemPromptMinVersion + `) or later, and a token with site-wide ` + "`owner`" + ` permissions.
`,
		Attributes: map[string]schema.Attribute{
			"system_prompt": schema.StringAttribute{
				CustomType: chatSystemPromptTextType{},
				Required:   true,
				MarkdownDescription: "The custom system prompt text, typically `file(\"${path.module}/system-prompt.md\")`. " +
					"Limited to 128 KiB after sanitization.",
				Validators: []validator.String{
					chatSystemPromptLengthValidator{},
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

func (r *ChatSystemPromptResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ChatSystemPromptResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.Client)
}

func (r *ChatSystemPromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ChatSystemPromptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	prompt, err := r.experimentalClient().GetChatSystemPrompt(ctx)
	if err != nil {
		resp.Diagnostics.Append(chatSystemPromptDiag("read", err)...)
		return
	}

	data.SystemPrompt = newChatSystemPromptTextValue(prompt.SystemPrompt)
	data.IncludeDefaultSystemPrompt = types.BoolValue(prompt.IncludeDefaultSystemPrompt)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ChatSystemPromptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ChatSystemPromptResourceModel
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

func (r *ChatSystemPromptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ChatSystemPromptResourceModel
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
func (r *ChatSystemPromptResource) put(ctx context.Context, action string, data *ChatSystemPromptResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	includeDefault := data.IncludeDefaultSystemPrompt.ValueBool()
	err := r.experimentalClient().UpdateChatSystemPrompt(ctx, codersdk.UpdateChatSystemPromptRequest{
		SystemPrompt:               data.SystemPrompt.ValueString(),
		IncludeDefaultSystemPrompt: &includeDefault,
	})
	if err != nil {
		diags.Append(chatSystemPromptDiag(action, err)...)
		return diags
	}

	return diags
}

func (r *ChatSystemPromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "deleting chat system prompt")

	// The API has no DELETE, so restore the deployment defaults.
	includeDefault := true
	err := r.experimentalClient().UpdateChatSystemPrompt(ctx, codersdk.UpdateChatSystemPromptRequest{
		SystemPrompt:               "",
		IncludeDefaultSystemPrompt: &includeDefault,
	})
	if err != nil {
		resp.Diagnostics.Append(chatSystemPromptDiag("reset", err)...)
		return
	}

	tflog.Trace(ctx, "successfully deleted chat system prompt")
}

func (r *ChatSystemPromptResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_chat_system_prompt is experimental. Changes are expected, and it is not recommended for production use.",
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

	var data ChatSystemPromptResourceModel
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
				"`terraform import coderd_chat_system_prompt.<name> chat_system_prompt` first.",
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
				"`terraform import coderd_chat_system_prompt.<name> chat_system_prompt` first.",
				live.IncludeDefaultSystemPrompt, data.IncludeDefaultSystemPrompt.ValueBool()),
		)
	}
}

func (r *ChatSystemPromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The singleton has no ID, but Read needs a non-null placeholder state.
	resp.Diagnostics.Append(resp.State.Set(ctx, ChatSystemPromptResourceModel{
		SystemPrompt:               newChatSystemPromptTextValue(""),
		IncludeDefaultSystemPrompt: types.BoolValue(true),
	})...)
}

func chatSystemPromptDiag(action string, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		diags.AddError(
			"Chat System Prompt Endpoint Unavailable",
			fmt.Sprintf("Unable to %s the chat system prompt: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later and a token with site-wide permissions; "+
				"upgrade the deployment or use a token with the required permissions. If neither is possible, "+
				"remove `coderd_chat_system_prompt` from your configuration. Original error: %s",
				action, "/api/experimental/chats/config/system-prompt", chatSystemPromptMinVersion, err),
		)
		return diags
	}

	diags.AddError("Client Error", fmt.Sprintf("unable to %s the chat system prompt, got error: %s", action, err))
	return diags
}

type chatSystemPromptLengthValidator struct{}

func (chatSystemPromptLengthValidator) Description(context.Context) string {
	return fmt.Sprintf("system prompt must be at most %d bytes after sanitization", maxChatSystemPromptBytes)
}

func (v chatSystemPromptLengthValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v chatSystemPromptLengthValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if got := len(codersdk.SanitizePromptText(req.ConfigValue.ValueString())); got > maxChatSystemPromptBytes {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"System Prompt Too Long",
			fmt.Sprintf("The system prompt is %d bytes after sanitization; the maximum Coder accepts is %d bytes (128 KiB).", got, maxChatSystemPromptBytes),
		)
	}
}

// Compares sanitized values to absorb server-side prompt normalization.
type chatSystemPromptTextType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = chatSystemPromptTextType{}

func (t chatSystemPromptTextType) String() string {
	return "chatSystemPromptTextType"
}

func (t chatSystemPromptTextType) Equal(o attr.Type) bool {
	if o, ok := o.(chatSystemPromptTextType); ok {
		return t.StringType.Equal(o.StringType)
	}
	return false
}

func (t chatSystemPromptTextType) ValueType(ctx context.Context) attr.Value {
	return chatSystemPromptTextValue{}
}

func (t chatSystemPromptTextType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return chatSystemPromptTextValue{StringValue: in}, nil
}

func (t chatSystemPromptTextType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T, expected basetypes.StringValue", attrValue)
	}
	return chatSystemPromptTextValue{StringValue: stringValue}, nil
}

type chatSystemPromptTextValue struct {
	basetypes.StringValue
}

var _ basetypes.StringValuableWithSemanticEquals = chatSystemPromptTextValue{}

func newChatSystemPromptTextValue(value string) chatSystemPromptTextValue {
	return chatSystemPromptTextValue{StringValue: basetypes.NewStringValue(value)}
}

func (v chatSystemPromptTextValue) Type(ctx context.Context) attr.Type {
	return chatSystemPromptTextType{}
}

func (v chatSystemPromptTextValue) Equal(o attr.Value) bool {
	if o, ok := o.(chatSystemPromptTextValue); ok {
		return v.StringValue.Equal(o.StringValue)
	}
	return false
}

func (v chatSystemPromptTextValue) StringSemanticEquals(ctx context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	newValue, ok := newValuable.(chatSystemPromptTextValue)
	if !ok {
		diags.AddError(
			"Semantic Equality Check Error",
			fmt.Sprintf("Expected chatSystemPromptTextValue, got: %T. Please report this issue to the provider developers.", newValuable),
		)
		return false, diags
	}
	return codersdk.SanitizePromptText(v.ValueString()) == codersdk.SanitizePromptText(newValue.ValueString()), diags
}
