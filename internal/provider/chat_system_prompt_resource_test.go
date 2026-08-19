package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// chatSystemPromptPath is the experimental endpoint backing the resource.
const chatSystemPromptPath = "/api/experimental/chats/config/system-prompt"

const chatSystemPromptResourceAddr = "coderd_chat_system_prompt.test"

// fakeChatCoderd is a minimal stand-in for a Coder deployment, serving just
// the endpoints the provider touches: the two Configure() calls plus the chat
// system prompt singleton.
//
// A fake rather than `integration.StartCoder` for the same reason as the
// OAuth2 settings tests: most of the matrix is about what the *provider* does
// with a given API response, including that the server-side sanitization of
// the stored prompt does not surface as drift. The fake sanitizes on PUT
// exactly like coderd does, which is the behavior under test.
type fakeChatCoderd struct {
	*httptest.Server

	mu            sync.Mutex
	prompt        string
	includeDflt   bool
	requests      []fakeRequest
	putPrompts    []string
	getStatus     int
	putStatus     int
	sanitizeOnPut bool
}

func newFakeChatCoderd(t *testing.T) *fakeChatCoderd {
	t.Helper()

	f := &fakeChatCoderd{includeDflt: true, sanitizeOnPut: true}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeChatCoderd) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, fakeRequest{Method: r.Method, Path: r.URL.Path})
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/api/v2/users/me":
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               "00000000-0000-0000-0000-000000000001",
			"username":         "admin",
			"organization_ids": []string{"00000000-0000-0000-0000-000000000002"},
		})
	case r.URL.Path == "/api/v2/entitlements":
		writeJSON(w, http.StatusOK, codersdk.Entitlements{
			Features: map[codersdk.FeatureName]codersdk.Feature{},
		})
	case r.URL.Path == chatSystemPromptPath && r.Method == http.MethodGet:
		f.mu.Lock()
		status, prompt, include := f.getStatus, f.prompt, f.includeDflt
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		writeJSON(w, http.StatusOK, codersdk.ChatSystemPromptResponse{
			SystemPrompt:               prompt,
			IncludeDefaultSystemPrompt: include,
			DefaultSystemPrompt:        "built-in prompt",
		})
	case r.URL.Path == chatSystemPromptPath && r.Method == http.MethodPut:
		f.mu.Lock()
		status := f.putStatus
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		var req codersdk.UpdateChatSystemPromptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, codersdk.Response{Message: "Bad Request."})
			return
		}
		f.mu.Lock()
		f.putPrompts = append(f.putPrompts, req.SystemPrompt)
		f.prompt = req.SystemPrompt
		if f.sanitizeOnPut {
			// Match coderd: the stored value is the sanitized value.
			f.prompt = codersdk.SanitizePromptText(req.SystemPrompt)
		}
		if req.IncludeDefaultSystemPrompt != nil {
			f.includeDflt = *req.IncludeDefaultSystemPrompt
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
	}
}

func chatSystemPromptConfig(url, prompt string, includeDefault *bool) string {
	include := ""
	if includeDefault != nil {
		include = fmt.Sprintf("\n\tinclude_default_system_prompt = %t", *includeDefault)
	}
	return oauth2SettingsProviderBlock(url) + fmt.Sprintf(`
resource "coderd_chat_system_prompt" "test" {
	system_prompt = %q%s
}
`, prompt, include)
}

// TestChatSystemPromptSemanticEquals covers the custom type directly: two
// prompts are the same setting iff they sanitize to the same string.
func TestChatSystemPromptSemanticEquals(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	for _, tc := range []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "prompt", "prompt", true},
		{"trailing newline", "prompt\n", "prompt", true},
		{"crlf vs lf", "a\r\nb", "a\nb", true},
		{"different text", "prompt a", "prompt b", false},
		{"whitespace-only difference inside a line", "a b", "a  b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, diags := newChatSystemPromptTextValue(tc.a).StringSemanticEquals(ctx, newChatSystemPromptTextValue(tc.b))
			require.False(t, diags.HasError())
			require.Equal(t, tc.want, got)
		})
	}
}

func TestChatSystemPromptLengthValidator(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// The sanitized form is what the server measures, so padding that
	// sanitizes away must not trip the validator.
	okPrompt := strings.Repeat("a", maxChatSystemPromptBytes) + "\n\n\n"
	tooLong := strings.Repeat("a", maxChatSystemPromptBytes+1)

	for _, tc := range []struct {
		name    string
		value   basetypes.StringValue
		wantErr bool
	}{
		{"at limit after sanitization", basetypes.NewStringValue(okPrompt), false},
		{"over limit", basetypes.NewStringValue(tooLong), true},
		{"null", basetypes.NewStringNull(), false},
		{"unknown", basetypes.NewStringUnknown(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := &validator.StringResponse{}
			chatSystemPromptLengthValidator{}.ValidateString(ctx, validator.StringRequest{
				Path:        pathSystemPrompt,
				ConfigValue: tc.value,
			}, resp)
			require.Equal(t, tc.wantErr, resp.Diagnostics.HasError())
		})
	}
}

// TestAccChatSystemPromptResource exercises the full lifecycle against the
// fake: create, refresh without drift despite server-side sanitization,
// update, and destroy resetting the deployment defaults.
func TestAccChatSystemPromptResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeChatCoderd(t)
	includeFalse := false

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with a trailing newline, the `file()` everyday case. The
			// fake stores the sanitized (trimmed) form, like coderd does.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a helpful agent.\n", nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						chatSystemPromptResourceAddr,
						tfjsonpath.New("include_default_system_prompt"),
						knownvalue.Bool(true),
					),
				},
			},
			// Re-planning the same config must be empty: the live value is
			// the sanitized form, which is semantically equal.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a helpful agent.\n", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// A real edit must show up and apply.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a very helpful agent.\n", &includeFalse),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						chatSystemPromptResourceAddr,
						tfjsonpath.New("include_default_system_prompt"),
						knownvalue.Bool(false),
					),
				},
			},
		},
	})

	// Destroy (run by resource.Test after the last step) resets the
	// deployment defaults rather than stranding the last-applied value.
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.prompt)
	require.True(t, f.includeDflt)
}

// TestAccChatSystemPromptImport adopts a live out-of-band prompt without
// issuing any PUT.
func TestAccChatSystemPromptImport(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeChatCoderd(t)
	f.mu.Lock()
	f.prompt = "configured in the dashboard"
	f.mu.Unlock()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             chatSystemPromptConfig(f.URL, "configured in the dashboard", nil),
				ResourceName:       chatSystemPromptResourceAddr,
				ImportState:        true,
				ImportStatePersist: true,
				// The ID is required by the CLI syntax but unused.
				ImportStateId: "chat_system_prompt",
			},
			// After import, the matching config plans clean: nothing to
			// overwrite, nothing to PUT.
			{
				Config: chatSystemPromptConfig(f.URL, "configured in the dashboard", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})

	// Adopting an existing value must never write it back. The only PUT in
	// the whole test is the framework's final `terraform destroy`, which
	// resets the prompt to empty.
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{""}, f.putPrompts)
}

// TestAccChatSystemPromptEndpointUnavailable pins the 404 diagnostic used for
// both old deployments and tokens without site-wide permissions.
func TestAccChatSystemPromptEndpointUnavailable(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeChatCoderd(t)
	f.mu.Lock()
	f.getStatus = http.StatusNotFound
	f.putStatus = http.StatusNotFound
	f.mu.Unlock()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: chatSystemPromptConfig(f.URL, "prompt", nil),
				// Terraform wraps error text, so match the summary line only.
				ExpectError: regexp.MustCompile("Chat System Prompt Endpoint Unavailable"),
			},
		},
	})
}

// TestAccChatSystemPromptRealCoderNoDrift runs the lifecycle against a real
// Coder instance. This is the live proof that the pinned SDK's sanitizer
// matches the deployed server's: the configured prompt deliberately carries a
// CRLF, a zero-width space, a run of blank lines, and a trailing newline, so
// if the deployment's sanitization ever diverges from the pinned
// codersdk.SanitizePromptText, the re-plan stops being empty.
func TestAccChatSystemPromptRealCoderNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "chat_system_prompt_acc")
	experimental := codersdk.NewExperimentalClient(client)

	messyPrompt := "You are a helpful agent.\r\nBe concise.\u200b\n\n\n\nAlways cite sources.\n"

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %[1]q
  token = %[2]q
}

resource "coderd_chat_system_prompt" "test" {
  system_prompt = %[3]q
}
`, client.URL.String(), client.SessionToken(), messyPrompt)

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						chatSystemPromptResourceAddr,
						tfjsonpath.New("include_default_system_prompt"),
						knownvalue.Bool(true),
					),
				},
			},
			// Re-planning the identical config must yield an empty plan: the
			// live value is whatever the real server sanitized and stored.
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})

	// The server must have stored the sanitized form, and the test-framework
	// destroy after the last step must have reset the deployment defaults.
	live, err := experimental.GetChatSystemPrompt(ctx)
	require.NoError(t, err)
	require.Empty(t, live.SystemPrompt)
	require.True(t, live.IncludeDefaultSystemPrompt)
}

// TestAccChatSystemPromptRealCoderImportNoDrift proves that adopting a prompt
// configured out of band (via the API, as the dashboard would) and re-planning
// the matching config is a clean, empty plan.
//
// The empty plan requires the config to byte-match the stored (sanitized)
// value: semantic equality preserves prior state on Read and Apply, but a
// Required attribute's planned value must equal config, so a config that
// differs only by sanitization shows one in-place normalization update after
// import and then converges. Both behaviors are pinned here.
func TestAccChatSystemPromptRealCoderImportNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "chat_system_prompt_import_acc")
	experimental := codersdk.NewExperimentalClient(client)

	// Configured out of band, so import (not a prior apply) seeds state.
	includeDefault := true
	require.NoError(t, experimental.UpdateChatSystemPrompt(ctx, codersdk.UpdateChatSystemPromptRequest{
		SystemPrompt:               "configured in the dashboard",
		IncludeDefaultSystemPrompt: &includeDefault,
	}))

	providerBlock := fmt.Sprintf(`
provider "coderd" {
  url   = %[1]q
  token = %[2]q
}
`, client.URL.String(), client.SessionToken())

	// Byte-matches the stored value, like trimspace(file(...)) would.
	cfgExact := providerBlock + `
resource "coderd_chat_system_prompt" "test" {
  system_prompt = "configured in the dashboard"
}
`
	// Differs only by a trailing newline, like a bare file(...) would.
	cfgTrailingNewline := providerBlock + `
resource "coderd_chat_system_prompt" "test" {
  system_prompt = "configured in the dashboard\n"
}
`

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             cfgExact,
				ResourceName:       chatSystemPromptResourceAddr,
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateId:      "chat_system_prompt",
			},
			// A config that byte-matches the stored value plans clean.
			{
				Config:   cfgExact,
				PlanOnly: true,
			},
			// A config that differs only by sanitization applies one
			// normalization update...
			{
				Config: cfgTrailingNewline,
			},
			// ...and then converges: refreshes keep the configured value via
			// semantic equality, so the re-plan is empty.
			{
				Config:   cfgTrailingNewline,
				PlanOnly: true,
			},
		},
	})
}

// TestChatSystemPromptModifyPlan covers the create-time overwrite advisories
// for both attributes. Every case carries the always-on experimental-resource
// warning, so the baseline warning count is 1.
func TestChatSystemPromptModifyPlan(t *testing.T) {
	t.Parallel()

	objType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"system_prompt":                 tftypes.String,
			"include_default_system_prompt": tftypes.Bool,
		},
	}
	// promptObject builds a state/plan value. A nil prompt yields a null
	// object, which is how the framework signals "no prior state" (a create)
	// and "no plan" (a destroy).
	promptObject := func(prompt any, include any) tftypes.Value {
		if prompt == nil && include == nil {
			return tftypes.NewValue(objType, nil)
		}
		return tftypes.NewValue(objType, map[string]tftypes.Value{
			"system_prompt":                 tftypes.NewValue(tftypes.String, prompt),
			"include_default_system_prompt": tftypes.NewValue(tftypes.Bool, include),
		})
	}
	nullObject := promptObject(nil, nil)

	for _, tc := range []struct {
		name string
		// livePrompt and liveInclude are the deployment's current values.
		livePrompt  string
		liveInclude bool
		// lookupStatus, when non-zero, makes the GET fail.
		lookupStatus int
		plan         tftypes.Value
		state        tftypes.Value
		wantWarnings int
	}{
		{
			name:         "WarnsWhenFirstApplyWouldOverwriteLivePrompt",
			livePrompt:   "configured in the dashboard",
			liveInclude:  true,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 2,
		},
		{
			name:         "WarnsWhenFirstApplyWouldFlipIncludeDefault",
			livePrompt:   "",
			liveInclude:  false,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 2,
		},
		{
			name:         "WarnsOnBothWhenBothDiffer",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 3,
		},
		{
			// A live prompt differing from the plan only by sanitization is
			// the same setting, not an overwrite.
			name:         "SilentWhenPromptMatchesModuloSanitization",
			livePrompt:   "from terraform",
			liveInclude:  true,
			plan:         promptObject("from terraform\n", true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
			// A never-configured deployment has nothing to lose.
			name:         "SilentOnGreenfieldDeployment",
			livePrompt:   "",
			liveInclude:  true,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
			// Not a create: an update already renders a real diff, and this
			// is also the first plan after `terraform import`.
			name:         "SilentWhenPriorStateExists",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			plan:         promptObject("from terraform", true),
			state:        promptObject("from terraform", true),
			wantWarnings: 1,
		},
		{
			name:         "SilentOnDestroyPlan",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			plan:         nullObject,
			state:        promptObject("from terraform", true),
			wantWarnings: 1,
		},
		{
			// A Required attribute is still unknown when it comes from an
			// input variable or module output. Defer rather than guess.
			name:         "SilentWhenPlannedPromptUnknown",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			plan:         promptObject(tftypes.UnknownValue, true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
			// Best-effort: a failed lookup must not turn into a plan error.
			// Create() makes the same call and reports it properly.
			name:         "SilentWhenLookupFails",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			lookupStatus: http.StatusForbidden,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newFakeChatCoderd(t)
			f.mu.Lock()
			f.prompt = tc.livePrompt
			f.includeDflt = tc.liveInclude
			f.getStatus = tc.lookupStatus
			f.mu.Unlock()

			serverURL, err := url.Parse(f.URL)
			require.NoError(t, err)
			client := codersdk.New(serverURL)
			client.SetSessionToken("test-token")

			r := &ChatSystemPromptResource{
				CoderdProviderData: &CoderdProviderData{Client: client},
			}

			schemaResp := &fwresource.SchemaResponse{}
			r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
			require.Empty(t, schemaResp.Diagnostics)
			s := schemaResp.Schema

			resp := &fwresource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s, Raw: tc.plan}}
			r.ModifyPlan(ctx, fwresource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: s, Raw: tc.plan},
				Plan:   tfsdk.Plan{Schema: s, Raw: tc.plan},
				State:  tfsdk.State{Schema: s, Raw: tc.state},
			}, resp)

			assert.Empty(t, resp.Diagnostics.Errors(), "a plan-time advisory must never fail the plan")
			assert.Len(t, resp.Diagnostics.Warnings(), tc.wantWarnings)
		})
	}
}
