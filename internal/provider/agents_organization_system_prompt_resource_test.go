package provider

import (
	"context"
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
	"github.com/google/uuid"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentsOrganizationSystemPromptResourceAddr = "coderd_agents_organization_system_prompt.test"

var (
	// Matches the organization the fake reports for /users/me.
	fakeDefaultOrgID = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	fakeOtherOrgID   = uuid.MustParse("00000000-0000-0000-0000-000000000003")
)

type fakeOrgPromptCoderd struct {
	*httptest.Server

	mu        sync.Mutex
	prompts   map[uuid.UUID]string
	puts      []fakeOrgPromptPut
	getStatus int
	putStatus int
}

type fakeOrgPromptPut struct {
	OrganizationID uuid.UUID
	SystemPrompt   string
}

func newFakeOrgPromptCoderd(t *testing.T) *fakeOrgPromptCoderd {
	t.Helper()

	f := &fakeOrgPromptCoderd{prompts: map[uuid.UUID]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

// organization resolves a path parameter the way coderd's organization
// middleware does: by UUID or name, with "default" and the nil UUID meaning
// the default organization.
func (*fakeOrgPromptCoderd) organization(param string) (codersdk.Organization, bool) {
	orgs := []codersdk.Organization{
		{MinimalOrganization: codersdk.MinimalOrganization{ID: fakeDefaultOrgID, Name: "coder"}, IsDefault: true},
		{MinimalOrganization: codersdk.MinimalOrganization{ID: fakeOtherOrgID, Name: "engineering"}},
	}
	for _, org := range orgs {
		if param == org.ID.String() || param == org.Name ||
			(org.IsDefault && (param == codersdk.DefaultOrganization || param == uuid.Nil.String())) {
			return org, true
		}
	}
	return codersdk.Organization{}, false
}

func (f *fakeOrgPromptCoderd) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v2/users/me":
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               "00000000-0000-0000-0000-000000000001",
			"username":         "admin",
			"organization_ids": []string{fakeDefaultOrgID.String()},
		})
		return
	case "/api/v2/entitlements":
		writeJSON(w, http.StatusOK, codersdk.Entitlements{
			Features: map[codersdk.FeatureName]codersdk.Feature{},
		})
		return
	}

	orgPath, isOrgPath := strings.CutPrefix(r.URL.Path, "/api/v2/organizations/")
	param, rest, _ := strings.Cut(orgPath, "/")
	org, found := f.organization(param)
	if !isOrgPath || !found {
		writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
		return
	}

	switch {
	case rest == "" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, org)
	case rest == "chats/config/system-prompt" && r.Method == http.MethodGet:
		f.mu.Lock()
		status, prompt := f.getStatus, f.prompts[org.ID]
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		writeJSON(w, http.StatusOK, codersdk.OrganizationChatSystemPromptResponse{SystemPrompt: prompt})
	case rest == "chats/config/system-prompt" && r.Method == http.MethodPut:
		f.mu.Lock()
		status := f.putStatus
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		var req codersdk.UpdateOrganizationChatSystemPromptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, codersdk.Response{Message: "Bad Request."})
			return
		}
		f.mu.Lock()
		f.puts = append(f.puts, fakeOrgPromptPut{OrganizationID: org.ID, SystemPrompt: req.SystemPrompt})
		f.prompts[org.ID] = codersdk.SanitizePromptText(req.SystemPrompt)
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
	}
}

func (f *fakeOrgPromptCoderd) setPrompt(orgID uuid.UUID, prompt string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts[orgID] = prompt
}

func (f *fakeOrgPromptCoderd) prompt(orgID uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompts[orgID]
}

func agentsOrganizationSystemPromptConfig(url, prompt string, orgID *uuid.UUID) string {
	org := ""
	if orgID != nil {
		org = fmt.Sprintf("\n\torganization_id = %q", orgID.String())
	}
	return oauth2SettingsProviderBlock(url) + fmt.Sprintf(`
resource "coderd_agents_organization_system_prompt" "test" {%s
	system_prompt = %q
}
`, org, prompt)
}

func TestAccAgentsOrganizationSystemPromptResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeOrgPromptCoderd(t)
	defaultOrg, otherOrg := fakeDefaultOrgID, fakeOtherOrgID

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: agentsOrganizationSystemPromptConfig(f.URL, "Use the engineering templates.\n", nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						agentsOrganizationSystemPromptResourceAddr,
						tfjsonpath.New("organization_id"),
						knownvalue.StringExact(defaultOrg.String()),
					),
				},
				Check: func(*terraform.State) error {
					assert.Equal(t, "Use the engineering templates.", f.prompt(defaultOrg))
					return nil
				},
			},
			{
				Config: agentsOrganizationSystemPromptConfig(f.URL, "Use the engineering templates.\n", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: agentsOrganizationSystemPromptConfig(f.URL, "Use the platform templates.", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(agentsOrganizationSystemPromptResourceAddr, plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					assert.Equal(t, "Use the platform templates.", f.prompt(defaultOrg))
					return nil
				},
			},
			{
				// Configuring the organization the resource already manages
				// must not replace it.
				Config: agentsOrganizationSystemPromptConfig(f.URL, "Use the platform templates.", &defaultOrg),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: agentsOrganizationSystemPromptConfig(f.URL, "Use the platform templates.", &otherOrg),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(agentsOrganizationSystemPromptResourceAddr, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: func(*terraform.State) error {
					assert.Empty(t, f.prompt(defaultOrg), "replacing the resource must clear the previous organization")
					assert.Equal(t, "Use the platform templates.", f.prompt(otherOrg))
					return nil
				},
			},
		},
	})

	require.Empty(t, f.prompt(otherOrg), "destroy must clear the organization instructions")
}

func TestAccAgentsOrganizationSystemPromptImport(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeOrgPromptCoderd(t)
	f.setPrompt(fakeOtherOrgID, "configured in the dashboard")
	otherOrg := fakeOtherOrgID

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             agentsOrganizationSystemPromptConfig(f.URL, "configured in the dashboard", &otherOrg),
				ResourceName:       agentsOrganizationSystemPromptResourceAddr,
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateId:      "engineering",
			},
			{
				Config: agentsOrganizationSystemPromptConfig(f.URL, "configured in the dashboard", &otherOrg),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})

	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []fakeOrgPromptPut{{OrganizationID: fakeOtherOrgID, SystemPrompt: ""}}, f.puts)
}

func TestAccAgentsOrganizationSystemPromptEndpointUnavailable(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	f := newFakeOrgPromptCoderd(t)
	setStatuses := func(get, put int) func() {
		return func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.getStatus, f.putStatus = get, put
		}
	}
	cfg := agentsOrganizationSystemPromptConfig(f.URL, "prompt", nil)
	unavailable := regexp.MustCompile("Organization System Prompt Endpoint Unavailable")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				PreConfig:   setStatuses(0, http.StatusNotFound),
				Config:      cfg,
				ExpectError: unavailable,
			},
			{
				PreConfig: setStatuses(0, 0),
				Config:    cfg,
			},
			{
				// Organizations are soft-deleted, so a refresh 404 must not
				// drop the resource from state.
				PreConfig:   setStatuses(http.StatusNotFound, 0),
				Config:      cfg,
				ExpectError: unavailable,
			},
			{
				PreConfig: setStatuses(0, 0),
				Config:    cfg,
			},
		},
	})
}

func TestAccAgentsOrganizationSystemPromptRealCoderNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_organization_system_prompt_acc")
	org, err := client.OrganizationByName(ctx, codersdk.DefaultOrganization)
	require.NoError(t, err)
	skipUnlessAgentsOrganizationSystemPromptEndpoint(ctx, t, client, org.ID)

	messyPrompt := "Use the engineering templates.\r\nPrefer Go.\u200b\n\n\n\nCite sources.\n"

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %[1]q
  token = %[2]q
}

resource "coderd_agents_organization_system_prompt" "test" {
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
						agentsOrganizationSystemPromptResourceAddr,
						tfjsonpath.New("organization_id"),
						knownvalue.StringExact(org.ID.String()),
					),
				},
				Check: func(*terraform.State) error {
					live, err := client.OrganizationChatSystemPrompt(ctx, org.ID)
					if err != nil {
						return err
					}
					assert.Equal(t, codersdk.SanitizePromptText(messyPrompt), live.SystemPrompt)
					return nil
				},
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})

	live, err := client.OrganizationChatSystemPrompt(ctx, org.ID)
	require.NoError(t, err)
	require.Empty(t, live.SystemPrompt)
}

// skipUnlessAgentsOrganizationSystemPromptEndpoint skips the test when the
// target deployment does not serve organization chat system prompts
// (Coder < 2.39).
func skipUnlessAgentsOrganizationSystemPromptEndpoint(ctx context.Context, t *testing.T, client *codersdk.Client, orgID uuid.UUID) {
	t.Helper()
	_, err := client.OrganizationChatSystemPrompt(ctx, orgID)
	if isNotFound(err) {
		t.Skipf("deployment does not support the organization chat system prompt endpoint: %s", err)
	}
	require.NoError(t, err, "probe organization chat system prompt endpoint")
}

func TestAgentsOrganizationSystemPromptModifyPlan(t *testing.T) {
	t.Parallel()

	objType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"organization_id": tftypes.String,
			"system_prompt":   tftypes.String,
		},
	}
	promptObject := func(orgID any, prompt any) tftypes.Value {
		return tftypes.NewValue(objType, map[string]tftypes.Value{
			"organization_id": tftypes.NewValue(tftypes.String, orgID),
			"system_prompt":   tftypes.NewValue(tftypes.String, prompt),
		})
	}
	nullObject := tftypes.NewValue(objType, nil)
	other := fakeOtherOrgID.String()

	for _, tc := range []struct {
		name          string
		defaultPrompt string
		otherPrompt   string
		lookupStatus  int
		config        tftypes.Value
		plan          tftypes.Value
		state         tftypes.Value
		wantWarnings  int
	}{
		{
			name:          "WarnsWhenDefaultOrganizationHasLivePrompt",
			defaultPrompt: "configured in the dashboard",
			config:        promptObject(nil, "from terraform"),
			plan:          promptObject(tftypes.UnknownValue, "from terraform"),
			state:         nullObject,
			wantWarnings:  2,
		},
		{
			name:         "WarnsWhenConfiguredOrganizationHasLivePrompt",
			otherPrompt:  "configured in the dashboard",
			config:       promptObject(other, "from terraform"),
			plan:         promptObject(other, "from terraform"),
			state:        nullObject,
			wantWarnings: 2,
		},
		{
			name:          "SilentWhenPromptMatchesModuloSanitization",
			defaultPrompt: "from terraform",
			config:        promptObject(nil, "from terraform\n"),
			plan:          promptObject(tftypes.UnknownValue, "from terraform\n"),
			state:         nullObject,
			wantWarnings:  1,
		},
		{
			name:         "SilentOnGreenfieldOrganization",
			config:       promptObject(nil, "from terraform"),
			plan:         promptObject(tftypes.UnknownValue, "from terraform"),
			state:        nullObject,
			wantWarnings: 1,
		},
		{
			name:          "SilentWhenPriorStateExists",
			defaultPrompt: "configured in the dashboard",
			config:        promptObject(nil, "from terraform"),
			plan:          promptObject(fakeDefaultOrgID.String(), "from terraform"),
			state:         promptObject(fakeDefaultOrgID.String(), "from terraform"),
			wantWarnings:  1,
		},
		{
			name:          "SilentOnDestroyPlan",
			defaultPrompt: "configured in the dashboard",
			config:        nullObject,
			plan:          nullObject,
			state:         promptObject(fakeDefaultOrgID.String(), "from terraform"),
			wantWarnings:  1,
		},
		{
			name:          "SilentWhenPlannedPromptUnknown",
			defaultPrompt: "configured in the dashboard",
			config:        promptObject(nil, tftypes.UnknownValue),
			plan:          promptObject(tftypes.UnknownValue, tftypes.UnknownValue),
			state:         nullObject,
			wantWarnings:  1,
		},
		{
			// An unknown ID reads as the nil UUID, which Coder resolves to
			// the default organization.
			name:          "SilentWhenOrganizationUnknown",
			defaultPrompt: "configured in the dashboard",
			config:        promptObject(tftypes.UnknownValue, "from terraform"),
			plan:          promptObject(tftypes.UnknownValue, "from terraform"),
			state:         nullObject,
			wantWarnings:  1,
		},
		{
			name:          "SilentWhenLookupFails",
			defaultPrompt: "configured in the dashboard",
			lookupStatus:  http.StatusNotFound,
			config:        promptObject(nil, "from terraform"),
			plan:          promptObject(tftypes.UnknownValue, "from terraform"),
			state:         nullObject,
			wantWarnings:  1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newFakeOrgPromptCoderd(t)
			f.setPrompt(fakeDefaultOrgID, tc.defaultPrompt)
			f.setPrompt(fakeOtherOrgID, tc.otherPrompt)
			f.mu.Lock()
			f.getStatus = tc.lookupStatus
			f.mu.Unlock()

			serverURL, err := url.Parse(f.URL)
			require.NoError(t, err)
			client := codersdk.New(serverURL)
			client.SetSessionToken("test-token")

			r := &AgentsOrganizationSystemPromptResource{
				CoderdProviderData: &CoderdProviderData{Client: client, DefaultOrganizationID: fakeDefaultOrgID},
			}

			schemaResp := &fwresource.SchemaResponse{}
			r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
			require.Empty(t, schemaResp.Diagnostics)
			s := schemaResp.Schema

			resp := &fwresource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s, Raw: tc.plan}}
			r.ModifyPlan(ctx, fwresource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: s, Raw: tc.config},
				Plan:   tfsdk.Plan{Schema: s, Raw: tc.plan},
				State:  tfsdk.State{Schema: s, Raw: tc.state},
			}, resp)

			assert.Empty(t, resp.Diagnostics.Errors(), "a plan-time advisory must never fail the plan")
			assert.Len(t, resp.Diagnostics.Warnings(), tc.wantWarnings)
		})
	}
}
