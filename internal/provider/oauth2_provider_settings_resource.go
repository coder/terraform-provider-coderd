package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &OAuth2ProviderSettingsResource{}
var _ resource.ResourceWithImportState = &OAuth2ProviderSettingsResource{}
var _ resource.ResourceWithModifyPlan = &OAuth2ProviderSettingsResource{}

// oauth2ProviderSettingsMinVersion is the first Coder release exposing
// `/api/v2/oauth2-provider/settings`. It is named in the error surfaced when
// that endpoint 404s, so an admin pointed at an older deployment gets an
// actionable message instead of a bare "not found".
const oauth2ProviderSettingsMinVersion = "2.35.0"

// oauth2ProviderSettingsExperiment is the coderd experiment gating the whole
// `/api/v2/oauth2-provider` route, settings included. While it is unset, route
// middleware refuses every request with a 403 before any handler runs, so the
// setting can be neither read nor changed. It is off by default and is not
// covered by `--experiments='*'`, so this is the state of a stock deployment.
const oauth2ProviderSettingsExperiment = "oauth2"

// oauth2ProviderSettingsDefaultDCR is the deployment default for
// `dynamic_client_registration_enabled`. A never-configured deployment reads
// back as `false`, and `Delete` restores this value because the API has no
// DELETE verb for the setting.
const oauth2ProviderSettingsDefaultDCR = false

// pathDynamicClientRegistrationEnabled anchors plan-time diagnostics to the
// attribute they are about.
var pathDynamicClientRegistrationEnabled = path.Root("dynamic_client_registration_enabled")

type OAuth2ProviderSettingsResource struct {
	*CoderdProviderData
}

// OAuth2ProviderSettingsResourceModel describes the resource data model.
type OAuth2ProviderSettingsResourceModel struct {
	DynamicClientRegistrationEnabled types.Bool `tfsdk:"dynamic_client_registration_enabled"`
}

func NewOAuth2ProviderSettingsResource() resource.Resource {
	return &OAuth2ProviderSettingsResource{}
}

func (r *OAuth2ProviderSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oauth2_provider_settings"
}

func (r *OAuth2ProviderSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Deployment-wide OAuth2 provider settings.

This setting is a deployment-wide singleton, so this resource can only be
declared once. Declaring it more than once is not an error: whichever block
applies last silently wins.

~> **Warning**
When adopting a deployment where this setting has already been configured out
of band (via ` + "`coder oauth2-provider dcr enable`" + ` or the deployment settings UI), run
` + "`terraform import`" + ` **before** your first ` + "`terraform apply`" + `. There is no prior state
for Terraform to diff against on a first apply, so the live value is overwritten
with your configured one without appearing as a change in the plan. A plan-time
warning is raised if this would disable Dynamic Client Registration where it is
currently enabled, but a warning does not block the apply.

~> **Warning**
` + "`terraform destroy`" + ` resets ` + "`dynamic_client_registration_enabled`" + ` to ` + "`false`" + `, the
deployment default. The API has no delete operation for this setting, so the
value cannot be returned to a "never configured" state.

-> Managing this setting is entirely optional: omit the resource to leave
Dynamic Client Registration alone. To read the current value without taking
ownership of it, use the ` + "`coderd_oauth2_provider_settings`" + ` data source instead.

~> **Warning**
This resource is only compatible with Coder version [` + oauth2ProviderSettingsMinVersion + `](https://github.com/coder/coder/releases/tag/v` + oauth2ProviderSettingsMinVersion + `) and later.

~> **Warning**
The deployment must have the ` + "`" + oauth2ProviderSettingsExperiment + "`" + ` experiment enabled (` + "`CODER_EXPERIMENTS=" + oauth2ProviderSettingsExperiment + "`" + ` or ` + "`--experiments=" + oauth2ProviderSettingsExperiment + "`" + `). It is **off by default**, and ` + "`--experiments='*'`" + ` does **not** enable it, so it must be named explicitly. Without it, ` + "`/api/v2/oauth2-provider/settings`" + ` returns ` + "`403`" + ` and this resource cannot manage the setting. Development builds of Coder bypass the check, so a ` + "`-devel`" + ` deployment works either way — which makes this easy to miss until you apply against a release.
`,
		Attributes: map[string]schema.Attribute{
			"dynamic_client_registration_enabled": schema.BoolAttribute{
				Required: true,
				MarkdownDescription: "Whether OAuth2 Dynamic Client Registration ([RFC 7591](https://datatracker.ietf.org/doc/html/rfc7591)) " +
					"is enabled for the deployment. When disabled, `POST /oauth2/register` is rejected and the " +
					"`registration_endpoint` is omitted from the authorization server metadata document.",
			},
		},
	}
}

func (r *OAuth2ProviderSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OAuth2ProviderSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Read Terraform prior state data into the model
	var data OAuth2ProviderSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	settings, err := r.Client.OAuth2ProviderSettings(ctx)
	if err != nil {
		// Deliberately not treated as "resource deleted": this setting is a
		// deployment singleton that always exists on a supported deployment,
		// so a 404 means the endpoint is missing, not the resource.
		resp.Diagnostics.Append(oauth2ProviderSettingsDiag("read", err)...)
		return
	}

	data.DynamicClientRegistrationEnabled = types.BoolValue(dcrEnabledOrDefault(settings))

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OAuth2ProviderSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	// Read Terraform plan data into the model
	var data OAuth2ProviderSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "creating oauth2 provider settings", map[string]any{
		"dynamic_client_registration_enabled": data.DynamicClientRegistrationEnabled.ValueBool(),
	})

	// Create and Update use a shared implementation: the underlying API is a
	// single idempotent PUT with no separate create semantics.
	resp.Diagnostics.Append(r.patch(ctx, data.DynamicClientRegistrationEnabled.ValueBool())...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully created oauth2 provider settings", map[string]any{
		"dynamic_client_registration_enabled": data.DynamicClientRegistrationEnabled.ValueBool(),
	})

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OAuth2ProviderSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Read Terraform plan data into the model
	var data OAuth2ProviderSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "updating oauth2 provider settings", map[string]any{
		"dynamic_client_registration_enabled": data.DynamicClientRegistrationEnabled.ValueBool(),
	})

	// Create and Update use a shared implementation
	resp.Diagnostics.Append(r.patch(ctx, data.DynamicClientRegistrationEnabled.ValueBool())...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully updated oauth2 provider settings", map[string]any{
		"dynamic_client_registration_enabled": data.DynamicClientRegistrationEnabled.ValueBool(),
	})

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OAuth2ProviderSettingsResource) patch(ctx context.Context, dcrEnabled bool) diag.Diagnostics {
	var diags diag.Diagnostics
	// Always send a non-nil pointer. The API treats an omitted field as "leave
	// the current value alone", which is the right default for a partial
	// update but wrong for this resource: it owns the value outright, so
	// omitting it would make Create/Update no-ops and, worse, turn Delete's
	// reset-to-default into a silent no-op.
	_, err := r.Client.PutOAuth2ProviderSettings(ctx, codersdk.OAuth2ProviderSettings{
		DynamicClientRegistrationEnabled: &dcrEnabled,
	})
	if err != nil {
		diags.Append(oauth2ProviderSettingsDiag("update", err)...)
	}
	return diags
}

func (r *OAuth2ProviderSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "deleting oauth2 provider settings", map[string]any{})

	// There is no DELETE endpoint for this setting: it is a `site_configs`
	// upsert. Reset to the deployment default instead, so `terraform destroy`
	// leaves the deployment in a well-defined state rather than stranding the
	// last-applied value with no Terraform record of it.
	//
	// If this fails, the appended error keeps the resource in state, so a
	// subsequent `terraform destroy` retries rather than the admin wrongly
	// believing DCR was reset.
	resp.Diagnostics.Append(r.patch(ctx, oauth2ProviderSettingsDefaultDCR)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Trace(ctx, "successfully deleted oauth2 provider settings", map[string]any{})
}

// ModifyPlan warns, at plan time, when a first apply is about to disable
// Dynamic Client Registration on a deployment where it is currently enabled.
//
// That is the one direction worth flagging. A create has no prior state to
// diff against, so Terraform renders it as a plain "will be created" with no
// hint that a live value is being discarded. Surfacing it during plan is what
// gives the admin a chance to `terraform import` instead, while nothing has
// been changed yet.
//
// Deliberately not symmetric: a live `false` is indistinguishable from "never
// configured", because the server coalesces both, so warning whenever the live
// value merely differs from the plan would fire on every greenfield apply
// (live `false`, config `true`) for no benefit.
func (r *OAuth2ProviderSettingsResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
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

	var data OAuth2ProviderSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A Required attribute can still be unknown when it comes from an input
	// variable or a module output. Defer rather than guess.
	if data.DynamicClientRegistrationEnabled.IsUnknown() || data.DynamicClientRegistrationEnabled.IsNull() {
		return
	}
	if data.DynamicClientRegistrationEnabled.ValueBool() {
		// Enabling never discards an out-of-band value.
		return
	}

	settings, err := r.Client.OAuth2ProviderSettings(ctx)
	if err != nil {
		// Best-effort advisory only. The error is not swallowed: Create()
		// makes the same call for real moments later and reports it there,
		// with the right wording for the operation that actually failed.
		// Raising it here instead would turn every unreachable-or-forbidden
		// deployment into a confusing plan failure about a warning.
		tflog.Debug(ctx, "skipping oauth2 provider settings plan-time check", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if !dcrEnabledOrDefault(settings) {
		return
	}

	resp.Diagnostics.AddAttributeWarning(
		pathDynamicClientRegistrationEnabled,
		"Overwriting an out-of-band value",
		"`dynamic_client_registration_enabled` is currently `true` on this deployment, and applying will set it "+
			"to `false`. Terraform has no prior state for this resource, so this change is not shown as a diff.\n\n"+
			"If you meant to adopt the deployment's existing value rather than overwrite it, run "+
			"`terraform import coderd_oauth2_provider_settings.<name> oauth2_provider_settings` first.",
	)
}

func (r *OAuth2ProviderSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// No identifying attribute exists to extract from req.ID: this resource is
	// a deployment-wide singleton and Read() takes no parameters. Terraform
	// calls Read() immediately after this to populate
	// dynamic_client_registration_enabled from the live API; the import ID
	// itself is required by Terraform's CLI syntax but otherwise unused.
	//
	// The framework requires at least one attribute be set for the import to
	// produce a non-null state object for Read() to overwrite.
	resp.Diagnostics.Append(resp.State.Set(ctx, OAuth2ProviderSettingsResourceModel{
		DynamicClientRegistrationEnabled: types.BoolValue(oauth2ProviderSettingsDefaultDCR),
	})...)
}

// dcrEnabledOrDefault reads the DCR flag out of a settings response.
//
// `codersdk.OAuth2ProviderSettings.DynamicClientRegistrationEnabled` is a
// pointer so a PUT can omit it to mean "leave this alone". On a GET the field
// is documented to always come back non-nil, so nil here means the deployment
// answered with something the contract says it never should. Fall back to the
// same default the server itself applies to a never-configured deployment
// rather than panicking or inventing a value.
func dcrEnabledOrDefault(settings codersdk.OAuth2ProviderSettings) bool {
	if settings.DynamicClientRegistrationEnabled == nil {
		return oauth2ProviderSettingsDefaultDCR
	}
	return *settings.DynamicClientRegistrationEnabled
}

// oauth2ProviderSettingsDiag converts a codersdk error from the OAuth2
// provider settings endpoint into a diagnostic. Both the resource and the data
// source route through here so a deployment that predates the endpoint
// produces the same actionable message either way.
func oauth2ProviderSettingsDiag(action string, err error) diag.Diagnostics {
	var diags diag.Diagnostics

	// Not isNotFound: that helper also maps a 400 "must be an existing uuid or
	// username" to not-found, which is meaningless for a parameterless
	// endpoint and would mislabel an unrelated bad request as a version
	// problem.
	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		diags.AddError(
			"Unsupported Coder Version",
			fmt.Sprintf("Unable to %s OAuth2 provider settings: the deployment returned 404 for %s. "+
				"This endpoint requires Coder version %s or later; upgrade the deployment, or remove "+
				"`coderd_oauth2_provider_settings` from your configuration. Original error: %s",
				action, "/api/v2/oauth2-provider/settings", oauth2ProviderSettingsMinVersion, err),
		)
		return diags
	}

	if isOAuth2ExperimentOff(err) {
		diags.AddError(
			"OAuth2 Experiment Not Enabled",
			fmt.Sprintf("Unable to %s OAuth2 provider settings: the deployment returned 403 because the `%s` "+
				"experiment is not enabled. `%s` is gated behind that experiment, so the setting can be "+
				"neither read nor changed while it is off. Enable it on the Coder deployment "+
				"(`CODER_EXPERIMENTS=%s`, or `--experiments=%s`) and restart it, or remove "+
				"`coderd_oauth2_provider_settings` from your configuration. Original error: %s",
				action,
				oauth2ProviderSettingsExperiment,
				"/api/v2/oauth2-provider/settings",
				oauth2ProviderSettingsExperiment,
				oauth2ProviderSettingsExperiment,
				err),
		)
		return diags
	}

	diags.AddError("Client Error", fmt.Sprintf("unable to %s OAuth2 provider settings, got error: %s", action, err))
	return diags
}

// isOAuth2ExperimentOff reports whether err is coderd's `httpmw.RequireExperiment`
// refusal for the OAuth2 experiment rather than an RBAC denial. Both arrive as a
// 403, and only the message distinguishes them: the experiment gate names the
// experiment, while an RBAC denial is the generic "Forbidden."
//
// Mirrors isWorkspaceSharingExperimentOff, which solves the same problem for the
// workspace-sharing experiment. Matching on message text is unavoidable — coderd
// reuses 403 for both — but it degrades safely: a reworded message falls through
// to the generic "Client Error" that this branch replaced, which still surfaces
// coderd's own text.
func isOAuth2ExperimentOff(err error) bool {
	var sdkErr *codersdk.Error
	if !errors.As(err, &sdkErr) {
		return false
	}
	if sdkErr.StatusCode() != http.StatusForbidden {
		return false
	}
	// Requiring both substrings covers `RequireExperiment`'s single-experiment
	// message ("... requires enabling the 'oauth2' experiment.") and its
	// multi-experiment form ("... the following experiments: oauth2, ...").
	// An RBAC denial carries neither.
	return strings.Contains(sdkErr.Message, oauth2ProviderSettingsExperiment) &&
		strings.Contains(sdkErr.Message, "experiment")
}
