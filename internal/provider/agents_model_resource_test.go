package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
	"text/template"
	"time"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
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
		ContextLimit:         types.Int64Value(200000),
		CompressionThreshold: types.Int64Value(70),
		ModelConfig:          newAgentsModelConfigValue(`{"max_output_tokens":8192,"temperature":0.7,"cost":{"input_price_per_million_tokens":"3"}}`),
	}

	var diags diag.Diagnostics
	req := plan.createRequest(&diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, req.AIProviderID)
	require.Equal(t, aiProviderID, *req.AIProviderID)
	require.Equal(t, "claude-3-5-sonnet-20241022", req.Model)
	require.Equal(t, "Claude 3.5 Sonnet", req.DisplayName)
	require.NotNil(t, req.Enabled)
	require.True(t, *req.Enabled)
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
		ContextLimit:         types.Int64Value(200000),
		CompressionThreshold: types.Int64Value(70),
		ModelConfig:          newAgentsModelConfigValue(`{"max_output_tokens":8192}`),
	}
	plan := state
	plan.ModelConfig = newAgentsModelConfigNull()

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Equal(t, &codersdk.ChatModelCallConfig{}, patch.ModelConfig, "clearing sends an empty object")
	// Unchanged fields are omitted from the patch.
	require.Nil(t, patch.AIProviderID)
	require.Empty(t, patch.Model)
	require.Empty(t, patch.DisplayName)
	require.Nil(t, patch.Enabled)
	require.Nil(t, patch.ContextLimit)
	require.Nil(t, patch.CompressionThreshold)
}

// A changed field appears in the update patch. ModelConfig is covered above;
// this locks the Enabled transition.
func TestAgentsModelUpdateRequestChangedFields(t *testing.T) {
	t.Parallel()

	state := AgentsModelResourceModel{
		AIProviderID:         UUIDValue(uuid.New()),
		Model:                types.StringValue("claude-3-5-sonnet-20241022"),
		DisplayName:          types.StringValue("Claude 3.5 Sonnet"),
		Enabled:              types.BoolValue(true),
		ContextLimit:         types.Int64Value(200000),
		CompressionThreshold: types.Int64Value(70),
		ModelConfig:          newAgentsModelConfigNull(),
	}
	plan := state
	plan.Enabled = types.BoolValue(false)

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.Enabled)
	require.False(t, *patch.Enabled, "a changed Enabled is sent")
}

func TestAgentsModelStateFromModelConfig(t *testing.T) {
	t.Parallel()

	modelConfigID := uuid.New()
	aiProviderID := uuid.New()
	createdAt := time.Unix(1700000000, 0)
	updatedAt := time.Unix(1700000600, 0)
	// max_output_tokens is MaxInt64 on purpose: it is not exactly representable
	// as a float64, so if the key-sorting step ever decoded numbers into float64
	// (instead of json.Number) it would corrupt this to ...808 and fail the
	// exact-bytes assertion below. This is the numeric-no-regression guard.
	remote := decodeAgentsModelConfigForTest(t, `{"max_output_tokens":9223372036854775807,"cost":{"input_price_per_million_tokens":"3"}}`)

	var diags diag.Diagnostics
	state := stateFromModelConfig(codersdk.ChatModelConfig{
		ID:                   modelConfigID,
		AIProviderID:         aiProviderID,
		Model:                "claude-3-5-sonnet-20241022",
		DisplayName:          "Claude 3.5 Sonnet",
		Enabled:              true,
		ContextLimit:         200000,
		CompressionThreshold: 70,
		ModelConfig:          remote,
		CreatedAt:            createdAt,
		UpdatedAt:            updatedAt,
	}, "anthropic", &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Equal(t, modelConfigID, state.ID.ValueUUID())
	require.Equal(t, aiProviderID, state.AIProviderID.ValueUUID())
	require.Equal(t, "anthropic", state.ProviderType.ValueString())
	require.Equal(t, "claude-3-5-sonnet-20241022", state.Model.ValueString())
	require.Equal(t, "Claude 3.5 Sonnet", state.DisplayName.ValueString())
	require.True(t, state.Enabled.ValueBool())
	require.EqualValues(t, 200000, state.ContextLimit.ValueInt64())
	require.EqualValues(t, 70, state.CompressionThreshold.ValueInt64())
	require.Equal(t, createdAt.Unix(), state.CreatedAt.ValueInt64())
	require.Equal(t, updatedAt.Unix(), state.UpdatedAt.ValueInt64())

	// State must store model_config with alphabetically-sorted keys (cost before
	// max_output_tokens, recursively) so it byte-matches the user's jsonencode
	// config; the SDK struct order would emit max_output_tokens first. Without
	// the byte match, every post-import plan spuriously flips updated_at to
	// "known after apply".
	require.Equal(t,
		`{"cost":{"input_price_per_million_tokens":"3"},"max_output_tokens":9223372036854775807}`,
		state.ModelConfig.ValueString(),
		"state stores model_config with sorted keys and exact number tokens to match jsonencode byte-for-byte")
}

func TestAgentsModelConfigSemanticEquals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		prior string
		next  string
		equal bool
	}{
		{
			name:  "decimal trailing zeros",
			prior: `{"cost":{"input_price_per_million_tokens":"3"}}`,
			next:  `{"cost":{"input_price_per_million_tokens":"3.00"}}`,
			equal: true,
		},
		{
			name:  "whitespace and key order",
			prior: `{"max_output_tokens":8192,"temperature":0.7}`,
			next:  "{\n  \"temperature\": 0.7,\n  \"max_output_tokens\": 8192\n}",
			equal: true,
		},
		{
			name:  "legacy top-level pricing keys fold into cost",
			prior: `{"cost":{"input_price_per_million_tokens":"3"}}`,
			next:  `{"input_price_per_million_tokens":"3"}`,
			equal: true,
		},
		{
			name:  "different values are not equal",
			prior: `{"max_output_tokens":8192}`,
			next:  `{"max_output_tokens":4096}`,
			equal: false,
		},
		{
			name:  "empty objects are equal",
			prior: `{}`,
			next:  "{\n}",
			equal: true,
		},
		{
			name:  "empty object is not equal to a populated config",
			prior: `{}`,
			next:  `{"max_output_tokens":8192}`,
			equal: false,
		},
		{
			// Non-object JSON cannot canonicalize through the SDK struct, so the
			// comparison falls back to jsontypes' JSON-level semantic equality.
			name:  "non-object json falls back to json equality",
			prior: `[1, 2]`,
			next:  `[1,2]`,
			equal: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			next := newAgentsModelConfigValue(tc.next)
			equal, diags := next.StringSemanticEquals(t.Context(), newAgentsModelConfigValue(tc.prior))
			require.False(t, diags.HasError(), diags.Errors())
			require.Equal(t, tc.equal, equal)
		})
	}
}

func TestAgentsModelConfigCanonicalJSON(t *testing.T) {
	t.Parallel()

	t.Run("empty object canonicalizes to empty object", func(t *testing.T) {
		t.Parallel()
		got, err := agentsModelConfigCanonicalJSON(`{}`)
		require.NoError(t, err)
		require.Equal(t, `{}`, got)
	})

	t.Run("keys are ordered deterministically", func(t *testing.T) {
		t.Parallel()
		// The equality approach relies on json.Marshal emitting struct fields in a
		// stable order, so lock that invariant: regardless of input ordering the
		// canonical form follows the ChatModelCallConfig field declaration order.
		got, err := agentsModelConfigCanonicalJSON(`{"temperature":0.7,"max_output_tokens":8192}`)
		require.NoError(t, err)
		require.Equal(t, `{"max_output_tokens":8192,"temperature":0.7}`, got)
	})

	t.Run("invalid json returns an error", func(t *testing.T) {
		t.Parallel()
		_, err := agentsModelConfigCanonicalJSON(`{`)
		require.Error(t, err)
	})

	t.Run("non-object json returns an error", func(t *testing.T) {
		t.Parallel()
		// A JSON array is valid JSON but cannot decode into the SDK struct; this is
		// the case that exercises the StringSemanticEquals fallback path.
		_, err := agentsModelConfigCanonicalJSON(`[1, 2]`)
		require.Error(t, err)
	})
}

// TestAgentsModelConfigUseStateIfSemanticallyEqual covers the plan modifier that
// fixes the perpetual import-path diff: the plugin framework never runs
// StringSemanticEquals on the config->plan path, so a key-order-only difference
// between Coder's struct-order state JSON and the alphabetical jsonencode config
// would otherwise re-plan forever. The modifier pins the plan to state only when
// the two canonicalize equal.
func TestAgentsModelConfigUseStateIfSemanticallyEqual(t *testing.T) {
	t.Parallel()

	mod := agentsModelConfigUseStateIfSemanticallyEqual{}
	planAfter := func(state, config, plan types.String) types.String {
		resp := planmodifier.StringResponse{PlanValue: plan}
		mod.PlanModifyString(t.Context(), planmodifier.StringRequest{
			StateValue:  state,
			ConfigValue: config,
			PlanValue:   plan,
		}, &resp)
		return resp.PlanValue
	}

	t.Run("key-order-only difference pins state", func(t *testing.T) {
		t.Parallel()
		// state is Coder's struct order; config is what jsonencode emits (sorted).
		state := types.StringValue(`{"max_output_tokens":8192,"temperature":0.7}`)
		config := types.StringValue(`{"temperature":0.7,"max_output_tokens":8192}`)
		require.Equal(t, state, planAfter(state, config, config))
	})

	t.Run("real value change is left alone", func(t *testing.T) {
		t.Parallel()
		state := types.StringValue(`{"temperature":0.7}`)
		config := types.StringValue(`{"temperature":0.2}`)
		require.Equal(t, config, planAfter(state, config, config))
	})

	t.Run("null state is left alone", func(t *testing.T) {
		t.Parallel()
		config := types.StringValue(`{"temperature":0.7}`)
		require.Equal(t, config, planAfter(types.StringNull(), config, config))
	})

	t.Run("unknown config is left alone", func(t *testing.T) {
		t.Parallel()
		state := types.StringValue(`{"temperature":0.7}`)
		require.Equal(t, types.StringUnknown(), planAfter(state, types.StringUnknown(), types.StringUnknown()))
	})
}

func TestAgentsModelDecodeConfig(t *testing.T) {
	t.Parallel()

	t.Run("null is omitted", func(t *testing.T) {
		t.Parallel()
		var diags diag.Diagnostics
		require.Nil(t, agentsModelDecodeConfig(newAgentsModelConfigNull(), &diags))
		require.False(t, diags.HasError(), diags.Errors())
	})

	t.Run("valid json decodes", func(t *testing.T) {
		t.Parallel()
		var diags diag.Diagnostics
		got := agentsModelDecodeConfig(newAgentsModelConfigValue(`{"max_output_tokens":8192}`), &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, got)
		require.NotNil(t, got.MaxOutputTokens)
		require.EqualValues(t, 8192, *got.MaxOutputTokens)
	})

	t.Run("invalid json reports a diagnostic", func(t *testing.T) {
		t.Parallel()
		var diags diag.Diagnostics
		require.Nil(t, agentsModelDecodeConfig(newAgentsModelConfigValue(`{`), &diags))
		require.True(t, diags.HasError())
	})
}

func TestAgentsModelConfigNotEmptyValidator(t *testing.T) {
	t.Parallel()

	v := agentsModelConfigNotEmptyValidator{}
	validate := func(t *testing.T, config types.String) diag.Diagnostics {
		resp := &validator.StringResponse{}
		v.ValidateString(t.Context(), validator.StringRequest{
			Path:        path.Root("model_config"),
			ConfigValue: config,
		}, resp)
		return resp.Diagnostics
	}

	t.Run("empty object is rejected", func(t *testing.T) {
		t.Parallel()
		require.True(t, validate(t, types.StringValue(`{}`)).HasError())
	})

	t.Run("empty object with whitespace is rejected", func(t *testing.T) {
		t.Parallel()
		require.True(t, validate(t, types.StringValue("{\n  \n}")).HasError())
	})

	t.Run("populated config is allowed", func(t *testing.T) {
		t.Parallel()
		require.False(t, validate(t, types.StringValue(`{"max_output_tokens":8192}`)).HasError())
	})

	t.Run("null is allowed", func(t *testing.T) {
		t.Parallel()
		require.False(t, validate(t, types.StringNull()).HasError())
	})

	t.Run("unknown is allowed", func(t *testing.T) {
		t.Parallel()
		require.False(t, validate(t, types.StringUnknown()).HasError())
	})

	t.Run("invalid json is left for the custom type to report", func(t *testing.T) {
		t.Parallel()
		require.False(t, validate(t, types.StringValue(`{`)).HasError())
	})

	t.Run("non-object json is rejected", func(t *testing.T) {
		t.Parallel()
		require.True(t, validate(t, types.StringValue(`[1,2]`)).HasError())
	})
}

func TestAgentsModelConfigNoDroppedKeysValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  types.String
		wantErr string
	}{
		{name: "recognized config", config: types.StringValue(`{"max_output_tokens":8192,"temperature":0.7}`)},
		{name: "unknown nested field", config: types.StringValue(`{"provider_options":{"anthropic":{"bogus_setting":"x"}}}`), wantErr: "bogus_setting"},
		{name: "null", config: types.StringNull()},
		{name: "unknown", config: types.StringUnknown()},
		// Invalid and non-object JSON are other validators' problem.
		{name: "invalid json", config: types.StringValue(`{`)},
		{name: "non-object json", config: types.StringValue(`[1,2]`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &validator.StringResponse{}
			agentsModelConfigNoDroppedKeysValidator{}.ValidateString(t.Context(), validator.StringRequest{
				Path:        path.Root("model_config"),
				ConfigValue: tt.config,
			}, resp)
			if tt.wantErr == "" {
				require.False(t, resp.Diagnostics.HasError())
				return
			}
			require.True(t, resp.Diagnostics.HasError())
			require.Contains(t, resp.Diagnostics[0].Detail(), tt.wantErr)
		})
	}
}

func TestAgentsModelResourceValidationDefersUnknownConfig(t *testing.T) {
	t.Parallel()

	// PlanOnly reaches provider Configure(), which fetches the current user
	// and entitlements, so use a mock server instead of an unreachable URL.
	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

variable "ai_provider_id" {
  type = string
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = var.ai_provider_id
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000
}
`
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// ai_provider_id is unknown during the validate walk even though
				// ConfigVariables supplies a concrete plan value.
				Config: cfg,
				ConfigVariables: config.Variables{
					"ai_provider_id": config.StringVariable(uuid.NewString()),
				},
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
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

	// Captured from the applied state so the import step can compare model_config semantically.
	var priorModelConfig string

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_agents_model.sonnet", "id"),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "ai_provider_id", aiProvider.ID.String()),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "provider_type", "anthropic"),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "model", cfg1.Model),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "display_name", cfg1.DisplayName),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "enabled", "true"),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "context_limit", "200000"),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "compression_threshold", "70"),
					testCheckAgentsModelConfig(8192, 0.7),
					resource.TestCheckResourceAttrWith("coderd_agents_model.sonnet", "model_config", func(value string) error {
						priorModelConfig = value
						return nil
					}),
				),
			},
			{
				ResourceName:      "coderd_agents_model.sonnet",
				ImportState:       true,
				ImportStateVerify: true,
				// Coder serializes model_config fields in struct order while jsonencode sorts them
				// alphabetically, so ImportStateVerify's byte comparison can't match it. Compare it
				// semantically via ImportStateCheck instead.
				ImportStateVerifyIgnore: []string{"model_config"},
				ImportStateCheck:        importStateCheckModelConfigEquivalent(&priorModelConfig),
			},
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "display_name", cfg2.DisplayName),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "context_limit", "180000"),
					resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "compression_threshold", "60"),
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

// TestAccAgentsModelResourceModelConfigNoDrift proves the custom type prevents a
// perpetual diff when Coder re-serializes the value (e.g. "3.00" comes back "3").
func TestAccAgentsModelResourceModelConfigNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_model_drift_acc", integration.UseLicense)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000

  model_config = jsonencode({
    temperature = 0.70
    cost = {
      input_price_per_million_tokens  = "3.00"
      output_price_per_million_tokens = "15.00"
    }
  })
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_agents_model.sonnet", "id"),
				),
			},
			{
				// Re-planning the identical config must yield an empty plan: this is
				// the live proof that decimal trailing zeros do not cause drift.
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

// TestAccAgentsModelResourceImportNoDrift proves that importing a model and
// re-planning the identical config is a clean, empty plan.
//
// The model is created out-of-band (so import, not a prior apply, is what seeds
// state) and then imported. Read stores model_config as alphabetically-sorted
// JSON (agentsModelConfigToState), which byte-matches the HCL config's jsonencode
// output. top_p/top_k are chosen because the SDK struct order (top_p, top_k)
// differs from jsonencode's alphabetical order (top_k, top_p): if state were
// stored in struct order, the byte mismatch would trip the framework's raw-byte
// plan guard (PlannedState.Raw.Equal(PriorState.Raw)) and flip the computed
// updated_at to "known after apply" on every plan — a perpetual, non-convergent
// diff. Sorting keys in state removes the mismatch, so the re-plan is empty.
func TestAccAgentsModelResourceImportNoDrift(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_model_import_acc", integration.UseLicense)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	// Create the model out-of-band so state is first populated by import (Read).
	exp := codersdk.NewExperimentalClient(client)
	created, err := exp.CreateChatModelConfig(ctx, codersdk.CreateChatModelConfigRequest{
		AIProviderID: &aiProvider.ID,
		Model:        "claude-3-5-sonnet-20241022",
		ContextLimit: ptr.Ref(int64(200000)),
		ModelConfig: &codersdk.ChatModelCallConfig{
			TopP: ptr.Ref(0.9),
			TopK: ptr.Ref(int64(40)),
		},
	})
	require.NoError(t, err, "create chat model config out-of-band")
	// WithoutCancel: t.Context() is already cancelled by the time cleanup runs.
	t.Cleanup(func() { _ = exp.DeleteChatModelConfig(context.WithoutCancel(t.Context()), created.ID) })

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000

  model_config = jsonencode({
    top_p = 0.9
    top_k = 40
  })
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Import seeds state with the sorted model_config JSON.
				Config:             cfg,
				ResourceName:       "coderd_agents_model.sonnet",
				ImportState:        true,
				ImportStateId:      created.ID.String(),
				ImportStatePersist: true,
			},
			{
				// Re-plan against the same config must be a clean no-op: the sorted
				// model_config in state byte-matches the jsonencode config, so it
				// does not drift and updated_at is not flipped to "known after
				// apply". PlanOnly fails if the plan is non-empty.
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}

// TestAccAgentsModelResourceEmptyModelConfig locks in the empty-config guard: an
// empty "{}" is rejected at plan time rather than tripping a post-apply error.
func TestAccAgentsModelResourceEmptyModelConfig(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_model_empty_acc", integration.UseLicense)
	aiProvider := createAccAgentsModelAIProvider(ctx, t, client)

	cfg := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = 200000

  model_config = jsonencode({})
}
`, client.URL.String(), client.SessionToken(), aiProvider.ID.String())

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg,
				ExpectError: regexp.MustCompile(`Empty model_config`),
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

resource "coderd_agents_model" "sonnet" {
  ai_provider_id         = "{{.AIProviderID}}"
  model                  = "{{.Model}}"
  display_name           = "{{.DisplayName}}"
  enabled                = true
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

// TestAccAgentsModelResourceProviderTypeRederive covers the provider_type plan
// modifier (useStateForUnknownUnlessChanged on ai_provider_id): provider_type
// stays known in the plan while ai_provider_id is unchanged, and recomputes when
// ai_provider_id points at a provider of a different type.
func TestAccAgentsModelResourceProviderTypeRederive(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_model_provider_type_acc", integration.UseLicense)
	anthropic := createAccAgentsModelAIProvider(ctx, t, client)
	openai := createAccAgentsModelAIProviderOfType(ctx, t, client, codersdk.CreateAIProviderRequest{
		Type:        codersdk.AIProviderTypeOpenAI,
		Name:        "openai-agents-model-acc",
		DisplayName: "OpenAI Agents Model Acceptance",
		Enabled:     true,
		BaseURL:     "https://api.openai.com",
		APIKeys:     []string{"sk-test-primary-000000"},
	})

	cfg := func(providerID string, contextLimit int) string {
		return fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_agents_model" "sonnet" {
  ai_provider_id = %q
  model          = "claude-3-5-sonnet-20241022"
  context_limit  = %d
}
`, client.URL.String(), client.SessionToken(), providerID, contextLimit)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(anthropic.ID.String(), 200000),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
				Check: resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "provider_type", "anthropic"),
			},
			{
				// Changing only context_limit (ai_provider_id unchanged) must keep
				// provider_type known in the plan instead of "known after apply".
				Config: cfg(anthropic.ID.String(), 180000),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue("coderd_agents_model.sonnet", tfjsonpath.New("provider_type"), knownvalue.StringExact("anthropic")),
					},
				},
				Check: resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "provider_type", "anthropic"),
			},
			{
				// Changing ai_provider_id to a provider of a different type must
				// re-derive provider_type: unknown in the plan, openai after apply.
				Config: cfg(openai.ID.String(), 180000),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectUnknownValue("coderd_agents_model.sonnet", tfjsonpath.New("provider_type")),
					},
				},
				Check: resource.TestCheckResourceAttr("coderd_agents_model.sonnet", "provider_type", "openai"),
			},
		},
	})
}

func createAccAgentsModelAIProviderOfType(ctx context.Context, t *testing.T, client *codersdk.Client, req codersdk.CreateAIProviderRequest) codersdk.AIProvider {
	t.Helper()
	provider, err := client.CreateAIProvider(ctx, req)
	require.NoError(t, err, "create AI provider for Agents model acceptance test")
	t.Cleanup(func() {
		_ = client.DeleteAIProvider(context.Background(), provider.ID.String())
	})
	return provider
}

func createAccAgentsModelAIProvider(ctx context.Context, t *testing.T, client *codersdk.Client) codersdk.AIProvider {
	t.Helper()
	return createAccAgentsModelAIProviderOfType(ctx, t, client, codersdk.CreateAIProviderRequest{
		Type:        codersdk.AIProviderTypeAnthropic,
		Name:        "anthropic-agents-model-acc",
		DisplayName: "Anthropic Agents Model Acceptance",
		Enabled:     true,
		BaseURL:     "https://api.anthropic.com",
		APIKeys:     []string{"sk-ant-api03-test-primary"},
	})
}

func decodeAgentsModelConfigForTest(t *testing.T, raw string) *codersdk.ChatModelCallConfig {
	t.Helper()
	var config codersdk.ChatModelCallConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &config))
	return &config
}

// importStateCheckModelConfigEquivalent verifies the imported model_config is
// semantically equal to want by canonicalizing both through the SDK type, since
// ImportStateVerify only compares bytes and Coder's field ordering differs from
// jsonencode's.
func importStateCheckModelConfigEquivalent(want *string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		wantCanonical, err := agentsModelConfigCanonicalJSON(*want)
		if err != nil {
			return fmt.Errorf("canonicalize expected model_config: %w", err)
		}
		for _, s := range states {
			got, ok := s.Attributes["model_config"]
			if !ok {
				continue
			}
			gotCanonical, err := agentsModelConfigCanonicalJSON(got)
			if err != nil {
				return fmt.Errorf("canonicalize imported model_config: %w", err)
			}
			if gotCanonical != wantCanonical {
				return fmt.Errorf("imported model_config %q not equivalent to %q", got, *want)
			}
			return nil
		}
		return fmt.Errorf("imported state has no resource with model_config")
	}
}

func testCheckAgentsModelConfig(maxOutputTokens int64, temperature float64) resource.TestCheckFunc {
	return resource.TestCheckResourceAttrWith("coderd_agents_model.sonnet", "model_config", func(value string) error {
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
