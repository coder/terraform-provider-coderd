package provider

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func TestDefaultAgentsModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	state := stateFromDefaultModelConfig(codersdk.ChatModelConfig{ID: id, IsDefault: true})
	require.Equal(t, defaultAgentsModelID, state.ID.ValueString())
	require.Equal(t, id, state.ModelID.ValueUUID())
	require.Equal(t, id.String(), state.ModelID.ValueString())
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
  model_id = var.model_id
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

func TestAccDefaultAgentsModelResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "default_agents_model_acc", integration.UseLicense)
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
  model_id = coderd_agents_model.%s.id
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String(), aiProvider.ID.String(), defaultModel)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("sonnet"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_default_agents_model.default", "id", "default"),
					resource.TestCheckResourceAttrPair("coderd_default_agents_model.default", "model_id", "coderd_agents_model.sonnet", "id"),
					checkServerDefaultMatchesResource(ctx, t, client),
				),
			},
			{
				// Re-point the default to opus. Coder demotes sonnet atomically in
				// the same operation, so exactly one model remains default.
				Config: cfg("opus"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("coderd_default_agents_model.default", "model_id", "coderd_agents_model.opus", "id"),
					checkServerDefaultMatchesResource(ctx, t, client),
				),
			},
			{
				// A steady-state re-plan must be empty (no perpetual diff).
				Config:   cfg("opus"),
				PlanOnly: true,
			},
			{
				// Import by the model_id UUID; Read reconciles to the current default.
				ResourceName:      "coderd_default_agents_model.default",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["coderd_default_agents_model.default"]
					if !ok {
						return "", fmt.Errorf("coderd_default_agents_model.default not found in state")
					}
					return rs.Primary.Attributes["model_id"], nil
				},
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
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	sonnet := createAccChatModelConfig(ctx, t, client, aiProvider.ID, "claude-3-5-sonnet-20241022")
	opus := createAccChatModelConfig(ctx, t, client, aiProvider.ID, "claude-3-opus-20240229")
	exp := codersdk.NewExperimentalClient(client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_default_agents_model" "default" {
  model_id = %q
}
`, client.URL.String(), client.SessionToken(), sonnet.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			// Destroying the pointer must not clear the server default: Coder still
			// reports exactly one default, and it remains the last model we selected.
			defaults := serverDefaultModelIDs(ctx, t, client)
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
					checkServerDefaultMatchesResource(ctx, t, client),
				),
			},
			{
				// Externally re-point the default to opus, then expect Terraform to
				// detect the drift on refresh and plan to restore sonnet.
				PreConfig: func() {
					_, err := exp.UpdateChatModelConfig(ctx, opus.ID, codersdk.UpdateChatModelConfigRequest{
						IsDefault: ptr.Ref(true),
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
					checkServerDefaultMatchesResource(ctx, t, client),
				),
			},
		},
	})
}

// createAccChatModelConfig creates a chat model config directly via the SDK so it
// exists independently of any Terraform-managed resource.
func createAccChatModelConfig(ctx context.Context, t *testing.T, client *codersdk.Client, aiProviderID uuid.UUID, model string) codersdk.ChatModelConfig {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	created, err := exp.CreateChatModelConfig(ctx, codersdk.CreateChatModelConfigRequest{
		AIProviderID: &aiProviderID,
		Model:        model,
		ContextLimit: ptr.Ref(int64(200000)),
	})
	require.NoError(t, err, "create chat model config out-of-band")
	// WithoutCancel: t.Context() is already cancelled by the time cleanup runs.
	t.Cleanup(func() { _ = exp.DeleteChatModelConfig(context.WithoutCancel(t.Context()), created.ID) })
	return created
}

// serverDefaultModelIDs returns the IDs of every model Coder reports as default.
// Coder enforces a single default, so a healthy deployment returns one ID.
func serverDefaultModelIDs(ctx context.Context, t *testing.T, client *codersdk.Client) []uuid.UUID {
	t.Helper()
	exp := codersdk.NewExperimentalClient(client)
	configs, err := exp.ListChatModelConfigs(ctx)
	require.NoError(t, err, "list chat model configs")
	var defaults []uuid.UUID
	for _, c := range configs {
		if c.IsDefault {
			defaults = append(defaults, c.ID)
		}
	}
	return defaults
}

// checkServerDefaultMatchesResource asserts Coder reports exactly one default
// model and that it matches the resource's model_id attribute in state.
func checkServerDefaultMatchesResource(ctx context.Context, t *testing.T, client *codersdk.Client) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		defaults := serverDefaultModelIDs(ctx, t, client)
		if len(defaults) != 1 {
			return fmt.Errorf("expected exactly one default model, got %d: %v", len(defaults), defaults)
		}
		rs, ok := s.RootModule().Resources["coderd_default_agents_model.default"]
		if !ok {
			return fmt.Errorf("coderd_default_agents_model.default not found in state")
		}
		if got := rs.Primary.Attributes["model_id"]; got != defaults[0].String() {
			return fmt.Errorf("server default %s does not match resource model_id %s", defaults[0], got)
		}
		return nil
	}
}
