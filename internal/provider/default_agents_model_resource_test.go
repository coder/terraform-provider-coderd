package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func TestDefaultAgentsModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	organizationID := uuid.New()
	modelID := uuid.New()
	state := stateFromDefaultModelConfig(codersdk.ChatModel{
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

// TestDefaultAgentsModelUpgradeStateV0 upgrades legacy (schema version 0)
// state, which held the constant id "default" and no organization_id, to
// version 1: id and organization_id become null (Read resolves them through
// the legacy compatibility route) and model_id is preserved.
func TestDefaultAgentsModelUpgradeStateV0(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	modelID := uuid.New()

	r := &DefaultAgentsModelResource{}
	upgrader, ok := r.UpgradeState(ctx)[0]
	require.True(t, ok)
	require.NotNil(t, upgrader.PriorSchema)
	require.NotNil(t, upgrader.StateUpgrader)

	priorRaw := tftypes.NewValue(upgrader.PriorSchema.Type().TerraformType(ctx), map[string]tftypes.Value{
		"id":       tftypes.NewValue(tftypes.String, "default"),
		"model_id": tftypes.NewValue(tftypes.String, modelID.String()),
	})

	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError(), schemaResp.Diagnostics)

	req := fwresource.UpgradeStateRequest{
		State: &tfsdk.State{Raw: priorRaw, Schema: *upgrader.PriorSchema},
	}
	resp := &fwresource.UpgradeStateResponse{
		State: tfsdk.State{
			Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
			Schema: schemaResp.Schema,
		},
	}
	upgrader.StateUpgrader(ctx, req, resp)
	require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

	var upgraded DefaultAgentsModelResourceModel
	require.False(t, resp.State.Get(ctx, &upgraded).HasError())
	require.True(t, upgraded.ID.IsNull())
	require.True(t, upgraded.OrganizationID.IsNull())
	require.Equal(t, modelID, upgraded.ModelID.ValueUUID())
}

// TestDefaultAgentsModelResourceValidationDefersUnknownConfig checks validation
// passes when model_id is unknown, like when it comes from an unset variable.
func TestDefaultAgentsModelResourceValidationDefersUnknownConfig(t *testing.T) {
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

resource "coderd_default_agents_model" "default" {
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

// TestDefaultAgentsModelResourceDefersUnknownOrganizationID checks planning
// succeeds when organization_id comes from another resource and is therefore
// unknown until apply.
func TestDefaultAgentsModelResourceDefersUnknownOrganizationID(t *testing.T) {
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

resource "coderd_default_agents_model" "default" {
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

// TestAccDefaultAgentsModelResourceUpgradeFromV0_0_23 creates schema version 0
// state with the last provider release that used the deployment-wide default
// model, then switches to the in-repository provider. The current provider must
// upgrade id = "default", recover the default organization during Read, and
// converge without changing the selected server-side model.
func TestAccDefaultAgentsModelResourceUpgradeFromV0_0_23(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "default_agents_model_upgrade_v0_0_23_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfDefaultAgentsModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)
	model := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")

	// The first model in an organization is automatically the default. Import it
	// with v0.0.23 rather than creating coderd_default_agents_model: that release's
	// unscoped PATCH route no longer exists in Coder 2.37, while its import and
	// compatibility-list Read paths still produce the exact released v0 state.
	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_default_agents_model" "default" {
  model_id = %q
}
`, client.URL.String(), client.SessionToken(), model.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest: true,
		PreCheck:   func() { testAccPreCheck(t) },
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ExternalProviders: map[string]resource.ExternalProvider{
					"coderd": {
						Source:            "coder/coderd",
						VersionConstraint: "= 0.0.23",
					},
				},
				ResourceName:       "coderd_default_agents_model.default",
				ImportState:        true,
				ImportStateId:      model.ID.String(),
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("expected one imported resource, got %d", len(states))
					}
					if got := states[0].Attributes["id"]; got != "default" {
						return fmt.Errorf("expected legacy id %q, got %q", "default", got)
					}
					if got := states[0].Attributes["model_id"]; got != model.ID.String() {
						return fmt.Errorf("expected legacy model_id %q, got %q", model.ID, got)
					}
					return nil
				},
			},
			{
				Config:                   cfg,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "id", organizationID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "organization_id", organizationID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "model_id", model.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_default_agents_model.default"),
				),
			},
			{
				Config:                   cfg,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				PlanOnly:                 true,
			},
		},
	})
}

func TestAccDefaultAgentsModelResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "default_agents_model_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfDefaultAgentsModelUnsupported(ctx, t, client, organizationID)
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

resource "coderd_default_agents_model" "default" {
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
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "id", organizationID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "organization_id", organizationID.String()),
					resource.TestCheckResourceAttrPair("coderd_default_agents_model.default", "model_id", "coderd_agents_model.sonnet", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_default_agents_model.default"),
				),
			},
			{
				// Re-point the default to opus. Coder demotes sonnet atomically in
				// the same operation, so exactly one model remains default.
				Config: cfg("opus"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("coderd_default_agents_model.default", "model_id", "coderd_agents_model.opus", "id"),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_default_agents_model.default"),
				),
			},
			{
				// A steady-state re-plan must be empty (no perpetual diff).
				Config:   cfg("opus"),
				PlanOnly: true,
			},
			{
				// Import by organization UUID; Read resolves its current default.
				ResourceName:      "coderd_default_agents_model.default",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     organizationID.String(),
			},
		},
	})
}

// TestAccDefaultAgentsModelResourceDriftAndDelete proves two things against
// models created out-of-band (so they outlive the Terraform resource):
//
//   - Read detects an external change to the default and Terraform reconciles
//     back to the configured model.
//   - Delete is a no-op: Coder keeps exactly one model marked default, so
//     destroying the pointer leaves the server's default untouched.
func TestAccDefaultAgentsModelResourceDriftAndDelete(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "default_agents_model_drift_acc", integration.UseLicense)
	organizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfDefaultAgentsModelUnsupported(ctx, t, client, organizationID)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	sonnet := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-5-sonnet-20241022")
	opus := createAccChatModel(ctx, t, client, organizationID, aiProvider.ID, "claude-3-opus-20240229")
	exp := codersdk.NewExperimentalClient(client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_default_agents_model" "default" {
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
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_default_agents_model.default"),
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
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "model_id", sonnet.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, organizationID, "coderd_default_agents_model.default"),
				),
			},
		},
	})
}

func TestAccDefaultAgentsModelResourceOrganizationIsolation(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "default_agents_model_org_isolation_acc", integration.UseLicense)
	defaultOrganizationID := accDefaultOrganizationID(ctx, t, client)
	skipIfDefaultAgentsModelUnsupported(ctx, t, client, defaultOrganizationID)

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

resource "coderd_default_agents_model" "default_org" {
  organization_id = %q
  model_id        = %q
}

resource "coderd_default_agents_model" "other_org" {
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
					resource.TestCheckResourceAttr("coderd_default_agents_model.default_org", "id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.default_org", "organization_id", defaultOrganizationID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.default_org", "model_id", defaultOrgSecond.ID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.other_org", "id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.other_org", "organization_id", otherOrganization.ID.String()),
					resource.TestCheckResourceAttr("coderd_default_agents_model.other_org", "model_id", otherOrgFirst.ID.String()),
					checkServerDefaultMatchesResource(ctx, t, client, defaultOrganizationID, "coderd_default_agents_model.default_org"),
					checkServerDefaultMatchesResource(ctx, t, client, otherOrganization.ID, "coderd_default_agents_model.other_org"),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

func skipIfDefaultAgentsModelUnsupported(ctx context.Context, t *testing.T, client *codersdk.Client, organizationID uuid.UUID) {
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
