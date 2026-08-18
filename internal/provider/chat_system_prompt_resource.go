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

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ChatSystemPromptResource{}
var _ resource.ResourceWithImportState = &ChatSystemPromptResource{}
var _ resource.ResourceWithModifyPlan = &ChatSystemPromptResource{}

// chatSystemPromptMinVersion is the first Coder release serving
// `/api/experimental/chats/config/system-prompt` (coder/coder#22857). It is
// named in the error surfaced when the endpoint 404s so an admin pointed at an
// older deployment gets an actionable message instead of a bare "not found".
const chatSystemPromptMinVersion = "2.32.0"

// maxChatSystemPromptBytes mirrors coderd's maxSystemPromptLenBytes (128 KiB,
// coderd/exp_chats.go). The server rejects longer prompts with a 400 at apply
// time; validating here fails the same way at plan time instead.
const maxChatSystemPromptBytes = 131072

// pathSystemPrompt anchors plan-time diagnostics to the attribute they are
// about.
var pathSystemPrompt = path.Root("system_prompt")

type ChatSystemPromptResource struct {
	*CoderdProviderData
}

// ChatSystemPromptResourceModel describes the resource data model.
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

Coder sanitizes the stored prompt (strips invisible Unicode characters, normalizes line endings, collapses runs of blank lines, and trims surrounding whitespace), and this resource compares values the same way, so a trailing newline from ` + "`file(...)`" + ` does not cause drift.

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
		// Deliberately not treated as "resource deleted": this setting is a
		// deployment singleton that always exists on a supported deployment,
		// so a 404 means the endpoint is missing, not the resource.
		resp.Diagnostics.Append(chatSystemPromptDiag("read", err)...)
		return
	}

	// The custom type's semantic equality keeps the prior (configured) value
	// when the live value differs only by sanitization.
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

	// Create and Update use a shared implementation: the underlying API is a
	// single idempotent PUT with no separate create semantics.
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

// put writes the planned prompt and include-default flag. The flag pointer is
// always non-nil: the API treats an omitted field as "leave the current value
// alone", which is the right default for a partial update but wrong for this
// resource, which owns the value outright (the attribute has a schema default,
// so the plan always carries a known value).
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

	// The PUT returns 204 with no body, so nothing to reconcile: state keeps
	// the configured value and the custom type's semantic equality absorbs
	// the server-side sanitization on the next Read.
	return diags
}

func (r *ChatSystemPromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "deleting chat system prompt")

	// There is no DELETE endpoint for this setting: it is a `site_configs`
	// upsert. Reset to the defaults of a never-configured deployment (empty
	// prompt, include-default true) so `terraform destroy` leaves the
	// deployment in a well-defined state.
	//
	// If this fails, the appended error keeps the resource in state, so a
	// subsequent `terraform destroy` retries rather than the admin wrongly
	// believing the prompt was reset.
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

// ModifyPlan emits the standard experimental-resource warning and, on a first
// apply, warns when a non-empty out-of-band prompt is about to be overwritten,
// giving the admin a chance to `terraform import` instead.
func (r *ChatSystemPromptResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_chat_system_prompt is experimental. Changes are expected, and it is not recommended for production use.",
	)

	// A destroy plan has a null plan. Nothing to advise on.
	if req.Plan.Raw.IsNull() {
		return
	}
	// Only a genuine create reaches the no-prior-state case this warns about.
	// `terraform import` populates state without ever running Create(), so
	// this correctly stays quiet on the first plan after an import.
	if !req.State.Raw.IsNull() {
		return
	}
	// Configure() has not run during the validate walk.
	if r.CoderdProviderData == nil {
		return
	}

	var data ChatSystemPromptResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A Required attribute can still be unknown when it comes from an input
	// variable or a module output. Defer rather than guess.
	if data.SystemPrompt.IsUnknown() || data.SystemPrompt.IsNull() {
		return
	}

	live, err := r.experimentalClient().GetChatSystemPrompt(ctx)
	if err != nil {
		// Best-effort advisory only. Create() makes the same call for real
		// moments later and reports the error there, with the right wording
		// for the operation that actually failed.
		tflog.Debug(ctx, "skipping chat system prompt plan-time check", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if live.SystemPrompt == "" {
		// Nothing configured out of band; nothing to lose.
		return
	}
	if sanitizePromptText(live.SystemPrompt) == sanitizePromptText(data.SystemPrompt.ValueString()) {
		// The planned prompt matches the live one.
		return
	}

	resp.Diagnostics.AddAttributeWarning(
		pathSystemPrompt,
		"Overwriting an out-of-band value",
		"This deployment already has a chat system prompt configured, and applying will overwrite it. "+
			"Terraform has no prior state for this resource, so this change is not shown as a diff.\n\n"+
			"If you meant to adopt the deployment's existing value rather than overwrite it, run "+
			"`terraform import coderd_chat_system_prompt.<name> chat_system_prompt` first.",
	)
}

func (r *ChatSystemPromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// No identifying attribute exists to extract from req.ID: this resource is
	// a deployment-wide singleton and Read() takes no parameters. Terraform
	// calls Read() immediately after this to populate both attributes from the
	// live API; the import ID itself is required by Terraform's CLI syntax but
	// otherwise unused.
	//
	// The framework requires at least one attribute be set for the import to
	// produce a non-null state object for Read() to overwrite.
	resp.Diagnostics.Append(resp.State.Set(ctx, ChatSystemPromptResourceModel{
		SystemPrompt:               newChatSystemPromptTextValue(""),
		IncludeDefaultSystemPrompt: types.BoolValue(true),
	})...)
}

// chatSystemPromptDiag converts a codersdk error from the chat system prompt
// endpoint into a diagnostic. Every CRUD path routes through here so a
// deployment that predates the endpoint produces the same actionable message
// whichever operation hit it first.
func chatSystemPromptDiag(action string, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	// Not isNotFound: that helper also maps a 400 "must be an existing uuid or
	// username" to not-found, which is meaningless for a parameterless
	// endpoint and would mislabel an unrelated bad request as a version
	// problem.
	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		diags.AddError(
			"Unsupported Coder Version",
			fmt.Sprintf("Unable to %s the chat system prompt: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later and a token with site-wide permissions; "+
				"upgrade the deployment, or remove `coderd_chat_system_prompt` from your configuration. "+
				"Original error: %s",
				action, "/api/experimental/chats/config/system-prompt", chatSystemPromptMinVersion, err),
		)
		return diags
	}

	// Every other failure passes coderd's own message straight through.
	diags.AddError("Client Error", fmt.Sprintf("unable to %s the chat system prompt, got error: %s", action, err))
	return diags
}

// chatSystemPromptLengthValidator rejects prompts whose sanitized form exceeds
// coderd's 128 KiB cap, failing at plan time with the same limit the server
// would enforce with a 400 at apply time.
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
	if got := len(sanitizePromptText(req.ConfigValue.ValueString())); got > maxChatSystemPromptBytes {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"System Prompt Too Long",
			fmt.Sprintf("The system prompt is %d bytes after sanitization; the maximum Coder accepts is %d bytes (128 KiB).", got, maxChatSystemPromptBytes),
		)
	}
}

// chatSystemPromptTextType is a string type whose values compare equal when
// their sanitized forms match, absorbing the server-side prompt sanitization
// (trailing newlines from `file(...)`, CRLF line endings, invisible
// characters) instead of reporting it as drift.
type chatSystemPromptTextType struct {
	basetypes.StringType
}

var _ basetypes.StringTypable = chatSystemPromptTextType{}

// String implements basetypes.StringTypable.
func (t chatSystemPromptTextType) String() string {
	return "chatSystemPromptTextType"
}

// Equal implements basetypes.StringTypable.
func (t chatSystemPromptTextType) Equal(o attr.Type) bool {
	if o, ok := o.(chatSystemPromptTextType); ok {
		return t.StringType.Equal(o.StringType)
	}
	return false
}

// ValueType implements basetypes.StringTypable.
func (t chatSystemPromptTextType) ValueType(ctx context.Context) attr.Value {
	return chatSystemPromptTextValue{}
}

// ValueFromString implements basetypes.StringTypable.
func (t chatSystemPromptTextType) ValueFromString(ctx context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return chatSystemPromptTextValue{StringValue: in}, nil
}

// ValueFromTerraform implements basetypes.StringTypable.
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

// Type implements basetypes.StringValuable.
func (v chatSystemPromptTextValue) Type(ctx context.Context) attr.Type {
	return chatSystemPromptTextType{}
}

// Equal implements basetypes.StringValuable.
func (v chatSystemPromptTextValue) Equal(o attr.Value) bool {
	if o, ok := o.(chatSystemPromptTextValue); ok {
		return v.StringValue.Equal(o.StringValue)
	}
	return false
}

// StringSemanticEquals implements basetypes.StringValuableWithSemanticEquals:
// two prompts are the same setting iff they sanitize to the same string.
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
	return sanitizePromptText(v.ValueString()) == sanitizePromptText(newValue.ValueString()), diags
}
