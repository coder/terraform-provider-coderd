package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func TestAgentsDefaultModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	organizationID := uuid.New()
	modelID := uuid.New()
	state := stateFromAgentsDefaultModelConfig(codersdk.ChatModel{
		ID:             modelID,
		OrganizationID: organizationID,
		IsDefault:      true,
	})
	require.Equal(t, organizationID, state.ID.ValueUUID())
	require.Equal(t, organizationID.String(), state.ID.ValueString())
	require.Equal(t, organizationID, state.OrganizationID.ValueUUID())
	require.Equal(t, organizationID.String(), state.OrganizationID.ValueString())
	require.Equal(t, modelID, state.ModelID.ValueUUID())
	require.Equal(t, modelID.String(), state.ModelID.ValueString())
}

func TestAgentsDefaultModelIDPlanModifier(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	oldOrganizationID := uuid.New()
	newOrganizationID := uuid.New()
	modelID := uuid.New()

	r := &AgentsDefaultModelResource{}
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError(), schemaResp.Diagnostics)

	idAttribute, ok := schemaResp.Schema.Attributes["id"].(schema.StringAttribute)
	require.True(t, ok)
	require.Len(t, idAttribute.PlanModifiers, 1)
	modifier := idAttribute.PlanModifiers[0]

	raw := func(id tftypes.Value, organizationID uuid.UUID) tftypes.Value {
		return tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), map[string]tftypes.Value{
			"id":              id,
			"organization_id": tftypes.NewValue(tftypes.String, organizationID.String()),
			"model_id":        tftypes.NewValue(tftypes.String, modelID.String()),
		})
	}
	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    raw(tftypes.NewValue(tftypes.String, oldOrganizationID.String()), oldOrganizationID),
	}

	for _, tc := range []struct {
		name                string
		plannedOrganization uuid.UUID
		want                types.String
	}{
		{
			name:                "retains id when organization is unchanged",
			plannedOrganization: oldOrganizationID,
			want:                types.StringValue(oldOrganizationID.String()),
		},
		{
			name:                "leaves id unknown when organization changes",
			plannedOrganization: newOrganizationID,
			want:                types.StringUnknown(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan := tfsdk.Plan{
				Schema: schemaResp.Schema,
				Raw:    raw(tftypes.NewValue(tftypes.String, tftypes.UnknownValue), tc.plannedOrganization),
			}
			resp := &planmodifier.StringResponse{PlanValue: types.StringUnknown()}
			modifier.PlanModifyString(ctx, planmodifier.StringRequest{
				ConfigValue: types.StringNull(),
				PlanValue:   types.StringUnknown(),
				StateValue:  types.StringValue(oldOrganizationID.String()),
				Plan:        plan,
				State:       state,
			}, resp)

			require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
			require.Equal(t, tc.want, resp.PlanValue)
		})
	}
}

func TestAgentsDefaultModelPatch404Diagnostics(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		targetCollectionStatus  int
		defaultCollectionStatus int
		wantSummary             string
	}{
		{
			name:                   "model missing",
			targetCollectionStatus: http.StatusOK,
			wantSummary:            "Default Agents Model Not Found or Inaccessible",
		},
		{
			name:                    "organization missing",
			targetCollectionStatus:  http.StatusNotFound,
			defaultCollectionStatus: http.StatusOK,
			wantSummary:             "Organization Not Found or Inaccessible",
		},
		{
			name:                    "unsupported endpoint",
			targetCollectionStatus:  http.StatusNotFound,
			defaultCollectionStatus: http.StatusNotFound,
			wantSummary:             "Unsupported Coder Version",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			targetOrganizationID := uuid.New()
			defaultOrganizationID := uuid.New()
			modelID := uuid.New()
			targetCollectionPath := fmt.Sprintf("/api/experimental/organizations/%s/chats/models", targetOrganizationID)
			defaultCollectionPath := fmt.Sprintf("/api/experimental/organizations/%s/chats/models", defaultOrganizationID)
			modelPath := fmt.Sprintf("%s/%s", targetCollectionPath, modelID)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch {
				case req.Method == http.MethodPatch && req.URL.Path == modelPath:
					writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
				case req.Method == http.MethodGet && req.URL.Path == targetCollectionPath:
					writeAgentsDefaultModelCollectionResponse(w, tc.targetCollectionStatus)
				case req.Method == http.MethodGet && req.URL.Path == defaultCollectionPath:
					writeAgentsDefaultModelCollectionResponse(w, tc.defaultCollectionStatus)
				default:
					writeJSON(w, http.StatusInternalServerError, codersdk.Response{Message: "unexpected request"})
				}
			}))
			t.Cleanup(srv.Close)

			r := newAgentsDefaultModelTestResource(t, srv.URL, defaultOrganizationID)
			_, err := r.setDefault(t.Context(), targetOrganizationID, modelID)
			require.Error(t, err)

			diags := r.agentsDefaultModelDiag(t.Context(), "set", targetOrganizationID, modelID, err)
			require.Len(t, diags.Errors(), 1)
			require.Equal(t, tc.wantSummary, diags.Errors()[0].Summary())
			require.Contains(t, diags.Errors()[0].Detail(), "Original error:")
			require.Contains(t, diags.Errors()[0].Detail(), "Not Found.")
			if tc.wantSummary == "Unsupported Coder Version" {
				require.Contains(t, diags.Errors()[0].Detail(), agentsDefaultModelMinVersion)
			} else {
				require.NotContains(t, diags.Errors()[0].Detail(), "requires Coder version")
			}
		})
	}
}

func TestAgentsDefaultModelReadCollection404(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                    string
		targetCollectionStatus  int
		defaultCollectionStatus int
		wantSummary             string
		wantRemoved             bool
	}{
		{
			name:                    "missing organization removes state",
			targetCollectionStatus:  http.StatusNotFound,
			defaultCollectionStatus: http.StatusOK,
			wantRemoved:             true,
		},
		{
			name:                    "unsupported endpoint retains state",
			targetCollectionStatus:  http.StatusNotFound,
			defaultCollectionStatus: http.StatusNotFound,
			wantSummary:             "Unsupported Coder Version",
		},
		{
			name:                   "empty supported collection removes state",
			targetCollectionStatus: http.StatusOK,
			wantRemoved:            true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			targetOrganizationID := uuid.New()
			defaultOrganizationID := uuid.New()
			modelID := uuid.New()
			targetCollectionPath := fmt.Sprintf("/api/experimental/organizations/%s/chats/models", targetOrganizationID)
			defaultCollectionPath := fmt.Sprintf("/api/experimental/organizations/%s/chats/models", defaultOrganizationID)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case targetCollectionPath:
					writeAgentsDefaultModelCollectionResponse(w, tc.targetCollectionStatus)
				case defaultCollectionPath:
					writeAgentsDefaultModelCollectionResponse(w, tc.defaultCollectionStatus)
				default:
					writeJSON(w, http.StatusInternalServerError, codersdk.Response{Message: "unexpected request"})
				}
			}))
			t.Cleanup(srv.Close)

			r := newAgentsDefaultModelTestResource(t, srv.URL, defaultOrganizationID)
			state := agentsDefaultModelTestState(t, r, AgentsDefaultModelResourceModel{
				ID:             UUIDValue(targetOrganizationID),
				OrganizationID: UUIDValue(targetOrganizationID),
				ModelID:        UUIDValue(modelID),
			})
			resp := &fwresource.ReadResponse{State: state}
			r.Read(t.Context(), fwresource.ReadRequest{State: state}, resp)

			require.Equal(t, tc.wantRemoved, resp.State.Raw.IsNull())
			if tc.wantSummary == "" {
				require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)
				return
			}
			require.Len(t, resp.Diagnostics.Errors(), 1)
			require.Equal(t, tc.wantSummary, resp.Diagnostics.Errors()[0].Summary())
			require.Contains(t, resp.Diagnostics.Errors()[0].Detail(), "Original error:")
			require.Contains(t, resp.Diagnostics.Errors()[0].Detail(), "Not Found.")
		})
	}
}

func newAgentsDefaultModelTestResource(t *testing.T, serverURL string, defaultOrganizationID uuid.UUID) *AgentsDefaultModelResource {
	t.Helper()

	parsedURL, err := url.Parse(serverURL)
	require.NoError(t, err)
	return &AgentsDefaultModelResource{data: &CoderdProviderData{
		Client:                codersdk.New(parsedURL),
		DefaultOrganizationID: defaultOrganizationID,
	}}
}

func agentsDefaultModelTestState(t *testing.T, r *AgentsDefaultModelResource, model AgentsDefaultModelResourceModel) tfsdk.State {
	t.Helper()

	ctx := t.Context()
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError(), schemaResp.Diagnostics)

	state := tfsdk.State{
		Schema: schemaResp.Schema,
		Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
	}
	require.False(t, state.Set(ctx, &model).HasError())
	return state
}

func writeAgentsDefaultModelCollectionResponse(w http.ResponseWriter, status int) {
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status != http.StatusOK {
		writeJSON(w, status, codersdk.Response{Message: statusMessage(status)})
		return
	}
	writeJSON(w, http.StatusOK, codersdk.OrganizationChatModelsResponse{})
}

// TestAgentsDefaultModelResourceValidationDefersUnknownConfig checks validation
// passes when model_id is unknown, like when it comes from an unset variable.
func TestAgentsDefaultModelResourceValidationDefersUnknownConfig(t *testing.T) {
	t.Parallel()

	// PlanOnly reaches provider Configure(), which fetches the current user
	// and entitlements, so use a mock server instead of an unreachable URL.
	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

variable "model_id" {
  type = string
}

resource "coderd_agents_default_model" "default" {
  organization_id = "` + uuid.NewString() + `"
  model_id        = var.model_id
}
`
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// model_id is unknown during the validate walk even though
				// ConfigVariables supplies a concrete plan value.
				Config: cfg,
				ConfigVariables: config.Variables{
					"model_id": config.StringVariable(uuid.NewString()),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// TestAgentsDefaultModelResourceDefersUnknownOrganizationID checks planning
// succeeds when organization_id comes from another resource and is therefore
// unknown until apply.
func TestAgentsDefaultModelResourceDefersUnknownOrganizationID(t *testing.T) {
	t.Parallel()

	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

variable "organization_id" {
  type = string
}

resource "terraform_data" "organization" {
  input = var.organization_id
}

resource "coderd_agents_default_model" "default" {
  organization_id = terraform_data.organization.output
  model_id        = "` + uuid.NewString() + `"
}
`
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigVariables: config.Variables{
					"organization_id": config.StringVariable(uuid.NewString()),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccAgentsDefaultModelResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	cfg := func(defaultModel string) string {
		return fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000
}

resource "coderd_agents_model" "opus" {
  ai_provider_id = %q
  model          = "claude-3-opus-20240229"
  context_limit  = 200000
}

resource "coderd_agents_default_model" "default" {
  organization_id = %q
  model_id        = coderd_agents_model.%s.id
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String(), aiProvider.ID.String(), organizationID.String(), defaultModel)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("sonnet"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "id", organizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "organization_id", organizationID.String()),
					resource.TestCheckResourceAttrPair("coderd_agents_default_model.default", "model_id", "coderd_agents_model.sonnet", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// Re-point the default to opus. Coder demotes sonnet atomically in
				// the same operation, so exactly one model remains default.
				Config: cfg("opus"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("coderd_agents_default_model.default", "model_id", "coderd_agents_model.opus", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// A steady-state re-plan must be empty (no perpetual diff).
				Config:   cfg("opus"),
				PlanOnly: true,
			},
			{
				// Import by organization UUID; Read resolves its current default.
				ResourceName:      "coderd_agents_default_model.default",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     organizationID.String(),
			},
		},
	})
}

// TestAccAgentsDefaultModelResourceDriftAndDelete proves two things against
// models created out-of-band (so they outlive the Terraform resource):
//
//   - Read detects an external change to the default and Terraform reconciles
//     back to the configured model.
//   - Delete is a no-op: Coder keeps exactly one model marked default, so
//     destroying the pointer leaves the server's default untouched.
func TestAccAgentsDefaultModelResourceDriftAndDelete(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_drift_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	sonnet := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	opus := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-opus-20240229")
	exp := codersdk.NewExperimentalClient(client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_default_model" "default" {
  organization_id = %q
  model_id        = %q
}
`, client.URL.String(), client.SessionToken(), organizationID.String(), sonnet.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Destroying the pointer must not clear the server default: Coder still
			// reports exactly one default, and it remains the last model we selected.
			defaults := serverDefaultModelIDs(ctx, t, client, organizationID)
			if len(defaults) != 1 {
				return fmt.Errorf("expected exactly one default model after destroy, got %d: %v", len(defaults), defaults)
			}
			if defaults[0] != sonnet.ID {
				return fmt.Errorf("expected default to remain %s after destroy, got %s", sonnet.ID, defaults[0])
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
			{
				// Externally re-point the default to opus, then expect Terraform to
				// detect the drift on refresh and plan to restore sonnet.
				PreConfig: func() {
					_, err := exp.UpdateChatModel(ctx, organizationID, opus.ID, codersdk.UpdateChatModelRequest{
						IsDefault: new(true),
					})
					require.NoError(t, err, "externally set opus as default")
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Re-applying reconciles the default back to sonnet.
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_agents_default_model.default"),
				),
			},
		},
	})
}

func TestAccAgentsDefaultModelResourceOrganizationIsolation(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_default_model_org_isolation_acc", integration.UseLicense)
	defaultOrganizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfAgentsDefaultModelUnsupported(ctx, t, client, defaultOrganizationID)

	otherOrganization, err := client.CreateOrganization(ctx, codersdk.CreateOrganizationRequest{
		Name:        "default-model-isolation",
		DisplayName: "Default Model Isolation",
	})
	require.NoError(t, err, "create second organization")
	t.Cleanup(func() {
		_ = client.DeleteOrganization(context.WithoutCancel(t.Context()), otherOrganization.ID.String())
	})

	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)
	createAccChatModel(ctx, t, client, defaultOrganizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	defaultOrgSecond := createAccChatModel(ctx, t, client, defaultOrganizationID, aiProvider.ID, "claude-3-opus-20240229")
	otherOrgFirst := createAccChatModel(ctx, t, client, otherOrganization.ID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	createAccChatModel(ctx, t, client, otherOrganization.ID, aiProvider.ID, "claude-3-opus-20240229")

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_default_model" "default_org" {
  organization_id = %q
  model_id        = %q
}

resource "coderd_agents_default_model" "other_org" {
  organization_id = %q
  model_id        = %q
}
`, client.URL.String(), client.SessionToken(), defaultOrganizationID.String(), defaultOrgSecond.ID.String(), otherOrganization.ID.String(), otherOrgFirst.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "organization_id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.default_org", "model_id", defaultOrgSecond.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "organization_id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_default_model.other_org", "model_id", otherOrgFirst.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, defaultOrganizationID, "coderd_agents_default_model.default_org"),
					checkServerDefaultMatchesResource(ctx, t, client, otherOrganization.ID, "coderd_agents_default_model.other_org"),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

func skipIfAgentsDefaultModelUnsupported(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID) {
	t.Helper()

	// Main devel builds report the previous minor's version, so a semver minimum
	// cannot distinguish them from releases that do not have this route.
	_, err := codersdk.NewExperimentalClient(client).ChatModels(ctx, organizationID)
	if err == nil {
		return
	}
	var sdkErr *codersdk.Error
	if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
		t.Skipf("deployment does not support org-scoped chat models")
	}
	require.NoError(t, err, "probe org-scoped chat models")
}

// createAccChatModel creates a chat model config directly via the SDK so it
// exists independently of any Terraform-managed resource.
func createAccChatModel(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID, aiProviderID uuid.UUID, model string) codersdk.ChatModel {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	created, err := exp.CreateChatModel(ctx, organizationID, codersdk.CreateChatModelRequest{
		AIProviderID: &aiProviderID,
		Model:        model,
		ContextLimit: new(int64(200000)),
	})
	require.NoError(t, err, "create chat model config out-of-band")
	// WithoutCancel: t.Context() is already cancelled by the time cleanup runs.
	t.Cleanup(func() { _ = exp.DeleteChatModel(context.WithoutCancel(t.Context()), organizationID, created.ID) })
	return created
}

// serverDefaultModelIDs returns the IDs of every model Coder reports as default
// in one organization. Coder enforces a single default per organization, so a
// healthy organization with models returns one ID.
func serverDefaultModelIDs(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID) []uuid.UUID {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	configs, err := exp.ChatModels(ctx, organizationID)
	require.NoError(t, err, "list chat models")
	var defaults []uuid.UUID
	for _, c := range configs.Models {
		if c.IsDefault {
			defaults = append(defaults, c.ID)
		}
	}
	return defaults
}

// checkServerDefaultMatchesResource asserts Coder reports exactly one default
// model in the organization and that it matches the named resource's model_id.
func checkServerDefaultMatchesResource(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID, resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		defaults := serverDefaultModelIDs(ctx, t, client, organizationID)
		if len(defaults) != 1 {
			return fmt.Errorf("expected exactly one default model in organization %s, got %d: %v", organizationID, len(defaults), defaults)
		}
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", resourceName)
		}
		if got := rs.Primary.Attributes["model_id"]; got != defaults[0].String() {
			return fmt.Errorf("server default %s does not match resource model_id %s", defaults[0], got)
		}
		return nil
	}
}
