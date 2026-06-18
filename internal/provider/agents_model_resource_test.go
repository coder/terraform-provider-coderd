package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"text/template"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAgentsModelCreateRequest(t *testing.T) {
	t.Parallel()

	aiProviderID := uuid.New()
	plan := AgentsModelResourceModel{
		AIProviderID:         UUIDValue(aiProviderID),
		Model:                types.StringValue("claude-3-5-sonnet-20241022"),
		DisplayName:          types.StringValue("Claude 3.5 Sonnet"),
		Enabled:              types.BoolValue(true),
		IsDefault:            types.BoolValue(true),
		ContextLimit:         types.Int64Value(200000),
		CompressionThreshold: types.Int64Value(70),
		ModelConfig:          jsontypes.NewNormalizedValue(`{"max_output_tokens":8192,"temperature":0.7,"cost":{"input_price_per_million_tokens":"3"}}`),
	}

	var diags diag.Diagnostics
	req := plan.createRequest(&diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Empty(t, req.Provider, "provider is derived server-side")
	require.NotNil(t, req.AIProviderID)
	require.Equal(t, aiProviderID, *req.AIProviderID)
	require.Equal(t, "claude-3-5-sonnet-20241022", req.Model)
	require.Equal(t, "Claude 3.5 Sonnet", req.DisplayName)
	require.NotNil(t, req.Enabled)
	require.True(t, *req.Enabled)
	require.NotNil(t, req.IsDefault)
	require.True(t, *req.IsDefault)
	require.NotNil(t, req.ContextLimit)
	require.EqualValues(t, 200000, *req.ContextLimit)
	require.NotNil(t, req.CompressionThreshold)
	require.EqualValues(t, 70, *req.CompressionThreshold)
	require.NotNil(t, req.ModelConfig)
	require.NotNil(t, req.ModelConfig.MaxOutputTokens)
	require.EqualValues(t, 8192, *req.ModelConfig.MaxOutputTokens)
	require.NotNil(t, req.ModelConfig.Cost)
	require.NotNil(t, req.ModelConfig.Cost.InputPricePerMillionTokens)
	require.Equal(t, "3", req.ModelConfig.Cost.InputPricePerMillionTokens.String())
}

func TestAgentsModelUpdateRequestClearsModelConfig(t *testing.T) {
	t.Parallel()

	state := AgentsModelResourceModel{
		AIProviderID:         UUIDValue(uuid.New()),
		Model:                types.StringValue("claude-3-5-sonnet-20241022"),
		DisplayName:          types.StringValue("Claude 3.5 Sonnet"),
		Enabled:              types.BoolValue(true),
		IsDefault:            types.BoolValue(true),
		ContextLimit:         types.Int64Value(200000),
		CompressionThreshold: types.Int64Value(70),
		ModelConfig:          jsontypes.NewNormalizedValue(`{"max_output_tokens":8192}`),
	}
	plan := state
	plan.ModelConfig = jsontypes.NewNormalizedNull()

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Equal(t, &codersdk.ChatModelCallConfig{}, patch.ModelConfig, "clearing sends an empty object")
}

func TestAgentsModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	modelConfigID := uuid.New()
	aiProviderID := uuid.New()
	createdAt := time.Unix(1700000000, 0)
	updatedAt := time.Unix(1700000600, 0)
	remote := decodeAgentsModelConfigForTest(t, `{"max_output_tokens":8192,"cost":{"input_price_per_million_tokens":"3"}}`)

	var diags diag.Diagnostics
	state := stateFromModelConfig(codersdk.ChatModelConfig{
		ID:                   modelConfigID,
		Provider:             "anthropic",
		AIProviderID:         &aiProviderID,
		Model:                "claude-3-5-sonnet-20241022",
		DisplayName:          "Claude 3.5 Sonnet",
		Enabled:              true,
		IsDefault:            true,
		ContextLimit:         200000,
		CompressionThreshold: 70,
		ModelConfig:          remote,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
	}, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Equal(t, modelConfigID, state.ID.ValueUUID())
	require.Equal(t, aiProviderID, state.AIProviderID.ValueUUID())
	require.Equal(t, "anthropic", state.ProviderType.ValueString())
	require.Equal(t, "claude-3-5-sonnet-20241022", state.Model.ValueString())
	require.Equal(t, "Claude 3.5 Sonnet", state.DisplayName.ValueString())
	require.True(t, state.Enabled.ValueBool())
	require.True(t, state.IsDefault.ValueBool())
	require.EqualValues(t, 200000, state.ContextLimit.ValueInt64())
	require.EqualValues(t, 70, state.CompressionThreshold.ValueInt64())
	require.Equal(t, createdAt.Unix(), state.CreatedAt.ValueInt64())
	require.Equal(t, updatedAt.Unix(), state.UpdatedAt.ValueInt64())

	expected, err := json.Marshal(remote)
	require.NoError(t, err)
	require.JSONEq(t, string(expected), state.ModelConfig.ValueString(), "state mirrors the config Coder returns")
}

func TestAccAgentsModelResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_model_acc", integration.UseLicense)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	cfg1 := testAccAgentsModelResourceConfig{
		URL:                  client.URL.String(),
		Token:                client.SessionToken(),
		AIProviderID:         aiProvider.ID.String(),
		Model:                "claude-3-5-sonnet-20241022",
		DisplayName:          "Claude 3.5 Sonnet",
		ContextLimit:         200000,
		CompressionThreshold: 70,
		MaxOutputTokens:      8192,
		Temperature:          "0.7",
	}
	cfg2 := cfg1
	cfg2.DisplayName = "Claude 3.5 Sonnet Updated"
	cfg2.ContextLimit = 180000
	cfg2.CompressionThreshold = 60
	cfg2.MaxOutputTokens = 4096
	cfg2.Temperature = "0.2"

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_experimental_agents_model.sonnet", "id"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "ai_provider_id", aiProvider.ID.String()),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "provider_type", "anthropic"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "model", cfg1.Model),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "display_name", cfg1.DisplayName),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "enabled", "true"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "is_default", "true"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "context_limit", "200000"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "compression_threshold", "70"),
					testCheckAgentsModelConfig(8192, 0.7),
				),
			},
			{
				ResourceName:            "coderd_experimental_agents_model.sonnet",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"model_config"},
			},
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "display_name", cfg2.DisplayName),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "context_limit", "180000"),
					resource.TestCheckResourceAttr("coderd_experimental_agents_model.sonnet", "compression_threshold", "60"),
					testCheckAgentsModelConfig(4096, 0.2),
				),
			},
			{
				Config:   cfg2.String(t),
				PlanOnly: true,
			},
		},
	})
}

type testAccAgentsModelResourceConfig struct {
	URL                  string
	Token                string
	AIProviderID         string
	Model                string
	DisplayName          string
	ContextLimit         int
	CompressionThreshold int
	MaxOutputTokens      int
	Temperature          string
}

func (c testAccAgentsModelResourceConfig) String(t *testing.T) string {
	t.Helper()
	const tpl = `
provider "coderd" {
  url   = "{{.URL}}"
  token = "{{.Token}}"
}

resource "coderd_experimental_agents_model" "sonnet" {
  ai_provider_id         = "{{.AIProviderID}}"
  model                  = "{{.Model}}"
  display_name           = "{{.DisplayName}}"
  enabled                = true
  is_default             = true
  context_limit          = {{.ContextLimit}}
  compression_threshold  = {{.CompressionThreshold}}

  model_config = jsonencode({
    max_output_tokens = {{.MaxOutputTokens}}
    temperature       = {{.Temperature}}
    cost = {
      input_price_per_million_tokens  = "3"
      output_price_per_million_tokens = "15"
    }
  })
}
`
	var out bytes.Buffer
	require.NoError(t, template.Must(template.New("agentsModelResource").Parse(tpl)).Execute(&out, c))
	return out.String()
}

func createAccAgentsModelAIProvider(ctx context.Context, t *testing.T, client *codersdk.Client) codersdk.AIProvider {
	t.Helper()
	provider, err := client.CreateAIProvider(ctx, codersdk.CreateAIProviderRequest{
		Type:        codersdk.AIProviderTypeAnthropic,
		Name:        "anthropic-agents-model-acc",
		DisplayName: "Anthropic Agents Model Acceptance",
		Enabled:     true,
		BaseURL:     "https://api.anthropic.com",
		APIKeys:     []string{"sk-ant-api03-test-primary"},
	})
	require.NoError(t, err, "create AI provider for Agents model acceptance test")
	t.Cleanup(func() {
		_ = client.DeleteAIProvider(context.Background(), provider.ID.String())
	})
	return provider
}

func decodeAgentsModelConfigForTest(t *testing.T, raw string) *codersdk.ChatModelCallConfig {
	t.Helper()
	var config codersdk.ChatModelCallConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	return &config
}

func testCheckAgentsModelConfig(maxOutputTokens int64, temperature float64) resource.TestCheckFunc {
	return resource.TestCheckResourceAttrWith("coderd_experimental_agents_model.sonnet", "model_config", func(value string) error {
		var config codersdk.ChatModelCallConfig
		if err := json.Unmarshal([]byte(value), &config); err != nil {
			return err
		}
		if config.MaxOutputTokens == nil || *config.MaxOutputTokens != maxOutputTokens {
			return fmt.Errorf("expected max_output_tokens %d, got %v", maxOutputTokens, config.MaxOutputTokens)
		}
		if config.Temperature == nil || *config.Temperature != temperature {
			return fmt.Errorf("expected temperature %f, got %v", temperature, config.Temperature)
		}
		return nil
	})
}
