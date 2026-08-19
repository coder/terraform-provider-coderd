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

const chatSystemPromptPath = "/api/experimental/chats/config/system-prompt"

const chatSystemPromptResourceAddr = "coderd_chat_system_prompt.test"

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
			{
				Config: chatSystemPromptConfig(f.URL, "You are a helpful agent.\n", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
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

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.prompt)
	require.True(t, f.includeDflt)
}

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
				ImportStateId:      "chat_system_prompt",
			},
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

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{""}, f.putPrompts)
}

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
				Config:      chatSystemPromptConfig(f.URL, "prompt", nil),
				ExpectError: regexp.MustCompile("Chat System Prompt Endpoint Unavailable"),
			},
		},
	})
}

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
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})

	live, err := experimental.GetChatSystemPrompt(ctx)
	require.NoError(t, err)
	require.Empty(t, live.SystemPrompt)
	require.True(t, live.IncludeDefaultSystemPrompt)
}

func TestAccChatSystemPromptRealCoderImportNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "chat_system_prompt_import_acc")
	experimental := codersdk.NewExperimentalClient(client)

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

	cfgExact := providerBlock + `
resource "coderd_chat_system_prompt" "test" {
  system_prompt = "configured in the dashboard"
}
`
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
			{
				Config:   cfgExact,
				PlanOnly: true,
			},
			{
				Config: cfgTrailingNewline,
			},
			{
				Config:   cfgTrailingNewline,
				PlanOnly: true,
			},
		},
	})
}

func TestChatSystemPromptModifyPlan(t *testing.T) {
	t.Parallel()

	objType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"system_prompt":                 tftypes.String,
			"include_default_system_prompt": tftypes.Bool,
		},
	}
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
		name         string
		livePrompt   string
		liveInclude  bool
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
			name:         "SilentWhenPromptMatchesModuloSanitization",
			livePrompt:   "from terraform",
			liveInclude:  true,
			plan:         promptObject("from terraform\n", true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
			name:         "SilentOnGreenfieldDeployment",
			livePrompt:   "",
			liveInclude:  true,
			plan:         promptObject("from terraform", true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
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
			name:         "SilentWhenPlannedPromptUnknown",
			livePrompt:   "configured in the dashboard",
			liveInclude:  false,
			plan:         promptObject(tftypes.UnknownValue, true),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
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
