package provider

import (
	"bytes"
	"os"
	"regexp"
	"testing"
	"text/template"

	aibridgeutils "github.com/coder/coder/v2/aibridge/utils"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/stretchr/testify/require"
)

func testAIProviderTerraformVersionChecks() []tfversion.TerraformVersionCheck {
	return []tfversion.TerraformVersionCheck{
		tfversion.SkipBelow(tfversion.Version1_11_0),
	}
}

func TestAccAIProviderResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ai_provider_acc", integration.UseLicense)

	cfg1 := testAccAIProviderResourceConfig{
		URL:              client.URL.String(),
		Token:            client.SessionToken(),
		OpenAIKey:        "sk-test-primary-000000",
		OpenAIKeyVersion: 1,
		BedrockBaseURL:   "https://bedrock-runtime.us-east-1.amazonaws.com",
	}
	cfg2 := cfg1
	cfg2.OpenAIKey = "sk-test-primary-111111"
	cfg2.OpenAIKeyVersion = 2
	// Changing base_url to a different region must re-derive settings.bedrock.region.
	cfg3 := cfg2
	cfg3.BedrockBaseURL = "https://bedrock-runtime.us-west-2.amazonaws.com"

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   testAIProviderTerraformVersionChecks(),
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.openai", "name", "openai-acc"),
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.openai", "api_key_masked", aibridgeutils.MaskSecret(cfg1.OpenAIKey)),
					resource.TestCheckNoResourceAttr("coderd_experimental_ai_provider.openai", "api_key_wo"),
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.bedrock", "settings.bedrock.region", "us-east-1"),
				),
			},
			{
				ResourceName:            "coderd_experimental_ai_provider.openai",
				ImportState:             true,
				ImportStateId:           "openai-acc",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"api_key_wo", "api_key_wo_version"},
			},
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.openai", "api_key_wo_version", "2"),
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.openai", "api_key_masked", aibridgeutils.MaskSecret(cfg2.OpenAIKey)),
				),
			},
			{
				Config: cfg3.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_experimental_ai_provider.bedrock", "settings.bedrock.region", "us-west-2"),
				),
			},
		},
	})
}

type testAccAIProviderResourceConfig struct {
	URL              string
	Token            string
	OpenAIKey        string
	OpenAIKeyVersion int
	BedrockBaseURL   string
}

func (c testAccAIProviderResourceConfig) String(t *testing.T) string {
	t.Helper()
	const tpl = `
provider "coderd" {
  url   = "{{.URL}}"
  token = "{{.Token}}"
}

resource "coderd_experimental_ai_provider" "openai" {
  type         = "openai"
  name         = "openai-acc"
  display_name = "OpenAI Acceptance"
  enabled      = true
  base_url     = "https://api.openai.com"

  api_key_wo         = "{{.OpenAIKey}}"
  api_key_wo_version = {{.OpenAIKeyVersion}}
}

resource "coderd_experimental_ai_provider" "bedrock" {
  type         = "bedrock"
  name         = "aws-bedrock-acc"
  display_name = "AWS Bedrock Acceptance"
  enabled      = true
  base_url     = "{{.BedrockBaseURL}}"

  settings = {
    bedrock = {
      # region omitted on purpose: it is derived from base_url.
      model            = "anthropic.claude-3-5-sonnet-20241022-v2:0"
      small_fast_model = "anthropic.claude-3-5-haiku-20241022-v1:0"
    }
  }
}
`
	var out bytes.Buffer
	require.NoError(t, template.Must(template.New("aiProviderResource").Parse(tpl)).Execute(&out, c))
	return out.String()
}

func TestAIProviderResourceSchemaValidation(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body      string
		wantError string
	}{
		"api key requires version": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type       = "openai"
  name       = "openai-test"
  base_url   = "https://api.openai.com"
  api_key_wo = "sk-test"
}
`,
			wantError: `api_key_wo_version`,
		},
		"api key cannot be empty": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type               = "openai"
  name               = "openai-test"
  base_url           = "https://api.openai.com"
  api_key_wo         = ""
  api_key_wo_version = 1
}
`,
			wantError: `string length must be at least 1`,
		},
		"bedrock known config requires region or credentials": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://example.com"

  settings = {
    bedrock = {}
  }
}
`,
			wantError: `Missing Bedrock Settings`,
		},
		"bedrock access key requires secret": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://bedrock-runtime.us-east-1.amazonaws.com"

  settings = {
    bedrock = {
      access_key_wo          = "AKIATEST"
      credentials_wo_version = 1
    }
  }
}
`,
			wantError: `access_key_secret_wo`,
		},
		"bedrock secret requires version": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://bedrock-runtime.us-east-1.amazonaws.com"

  settings = {
    bedrock = {
      access_key_wo        = "AKIATEST"
      access_key_secret_wo = "secret"
    }
  }
}
`,
			wantError: `credentials_wo_version`,
		},
		"api key rejected for copilot": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type               = "copilot"
  name               = "copilot-test"
  base_url           = "https://api.githubcopilot.com"
  api_key_wo         = "sk-test"
  api_key_wo_version = 1
}
`,
			wantError: "must not be configured when `type` is `copilot`",
		},
		"settings.bedrock rejected for openai": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "openai"
  name     = "openai-test"
  base_url = "https://api.openai.com"

  settings = {
    bedrock = {
      region = "us-east-1"
    }
  }
}
`,
			wantError: "only valid when `type` is `anthropic` or `bedrock`",
		},
		"invalid base url": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "openai"
  name     = "openai-test"
  base_url = "not-a-url"
}
`,
			wantError: `Invalid Base URL`,
		},
		"api key rejected for anthropic with bedrock settings": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type               = "anthropic"
  name               = "anthropic-test"
  base_url           = "https://api.anthropic.com"
  api_key_wo         = "sk-test"
  api_key_wo_version = 1

  settings = {
    bedrock = {
      region = "us-east-1"
    }
  }
}
`,
			wantError: "settings.bedrock",
		},
		"empty settings rejected for non-bedrock": {
			body: `resource "coderd_experimental_ai_provider" "test" {
  type     = "openai"
  name     = "openai-test"
  base_url = "https://api.openai.com"

  settings = {}
}
`,
			wantError: `Invalid Settings`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				TerraformVersionChecks:   testAIProviderTerraformVersionChecks(),
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      testAIProviderValidationConfig(tc.body),
						ExpectError: regexp.MustCompile(tc.wantError),
					},
				},
			})
		})
	}
}

func testAIProviderValidationConfig(body string) string {
	return testAIProviderValidationConfigWithURL("http://127.0.0.1", body)
}

func testAIProviderValidationConfigWithURL(url, body string) string {
	return `provider "coderd" {
  url   = "` + url + `"
  token = "test-token"
}

` + body
}

func TestAIProviderResourceValidationDefersUnknownBedrockConfig(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		body      string
		variables config.Variables
	}{
		"base url": {
			body: `variable "base_url" {
  type = string
}

resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = var.base_url

  settings = {
    bedrock = {}
  }
}
`,
			variables: config.Variables{
				"base_url": config.StringVariable("https://bedrock-runtime.us-east-1.amazonaws.com"),
			},
		},
		"credentials": {
			body: `variable "access_key" {
  type = string
}

variable "secret" {
  type = string
}

resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://example.com"

  settings = {
    bedrock = {
      access_key_wo          = var.access_key
      access_key_secret_wo   = var.secret
      credentials_wo_version = 1
    }
  }
}
`,
			variables: config.Variables{
				"access_key": config.StringVariable("AKIATEST"),
				"secret":     config.StringVariable("secret"),
			},
		},
		"region": {
			body: `variable "region" {
  type = string
}

resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://example.com"

  settings = {
    bedrock = {
      region = var.region
    }
  }
}
`,
			variables: config.Variables{
				"region": config.StringVariable("us-east-1"),
			},
		},
		"settings object": {
			body: `variable "settings" {
  type = object({
    bedrock = object({
      region = optional(string)
    })
  })
}

resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://example.com"

  settings = var.settings
}
`,
			variables: config.Variables{
				"settings": config.ObjectVariable(map[string]config.Variable{
					"bedrock": config.ObjectVariable(map[string]config.Variable{
						"region": config.StringVariable("us-east-1"),
					}),
				}),
			},
		},
		"bedrock object": {
			body: `variable "bedrock" {
  type = object({
    region = optional(string)
  })
}

resource "coderd_experimental_ai_provider" "test" {
  type     = "bedrock"
  name     = "bedrock-test"
  base_url = "https://example.com"

  settings = {
    bedrock = var.bedrock
  }
}
`,
			variables: config.Variables{
				"bedrock": config.ObjectVariable(map[string]config.Variable{
					"region": config.StringVariable("us-east-1"),
				}),
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// PlanOnly reaches provider Configure(), which fetches the current user
			// and entitlements, so use a mock server instead of an unreachable URL.
			srv := newMockServer(nil)
			defer srv.Close()

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				TerraformVersionChecks:   testAIProviderTerraformVersionChecks(),
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						// Required variables are unknown during ValidateResourceConfig,
						// even though ConfigVariables supplies concrete plan values.
						Config:          testAIProviderValidationConfigWithURL(srv.URL, tc.body),
						ConfigVariables: tc.variables,
						PlanOnly:        true,
						// PlanOnly expects an empty plan unless this is set.
						ExpectNonEmptyPlan: true,
					},
				},
			})
		})
	}
}

func TestAIProviderCreateRequestBedrockWithoutCredentials(t *testing.T) {
	t.Parallel()

	plan := AIProviderResourceModel{
		Type:        types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		Name:        types.StringValue("aws-bedrock"),
		DisplayName: types.StringUnknown(),
		Enabled:     types.BoolValue(true),
		BaseURL:     types.StringValue("https://bedrock-runtime.us-east-1.amazonaws.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:         types.StringUnknown(),
			Model:          types.StringValue("anthropic.claude-3-5-sonnet-20241022-v2:0"),
			SmallFastModel: types.StringValue("anthropic.claude-3-5-haiku-20241022-v1:0"),
		}},
	}
	// Copy the nested Bedrock value so mutating config doesn't alias plan's
	// pointer; plan keeps its unknown region while config supplies a null one.
	config := plan
	config.DisplayName = types.StringNull()
	configBedrock := *plan.Settings.Bedrock
	configBedrock.Region = types.StringNull()
	config.Settings = &AIProviderSettingsModel{Bedrock: &configBedrock}

	var diags diag.Diagnostics
	req := plan.createRequest(config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Empty(t, req.APIKeys)
	require.NotNil(t, req.Settings.Bedrock)
	require.Equal(t, "us-east-1", req.Settings.Bedrock.Region)
	require.Nil(t, req.Settings.Bedrock.AccessKey)
	require.Nil(t, req.Settings.Bedrock.AccessKeySecret)
}

func TestAIProviderUpdateRejectsDroppingBedrockSettings(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		Type:        types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		DisplayName: types.StringValue("AWS Bedrock"),
		Enabled:     types.BoolValue(true),
		BaseURL:     types.StringValue("https://bedrock-runtime.us-east-1.amazonaws.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region: types.StringValue("us-east-1"),
		}},
	}
	plan := state
	plan.Settings = nil

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, plan, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.Settings)
	require.Nil(t, patch.Settings.Bedrock)

	plan.validateEffectiveUpdateState(state, plan, &diags)
	require.True(t, diags.HasError())
	require.Contains(t, diags.Errors()[0].Summary(), "Missing Bedrock Settings")
}

func TestAIProviderUpdateRejectsClearingOnlyBedrockCredentials(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		Type:    types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		Enabled: types.BoolValue(true),
		BaseURL: types.StringValue("https://example.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringNull(),
			CredentialsWOVersion: types.Int64Value(1),
		}},
	}
	plan := AIProviderResourceModel{
		Type:    state.Type,
		Enabled: state.Enabled,
		BaseURL: state.BaseURL,
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringNull(),
			CredentialsWOVersion: types.Int64Value(2),
		}},
	}
	config := AIProviderResourceModel{
		Type:    plan.Type,
		Enabled: plan.Enabled,
		BaseURL: plan.BaseURL,
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringNull(),
			AccessKeyWO:          types.StringValue(""),
			AccessKeySecretWO:    types.StringValue(""),
			CredentialsWOVersion: types.Int64Value(2),
		}},
	}

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.Settings)
	require.NotNil(t, patch.Settings.Bedrock)
	require.False(t, patch.Settings.Bedrock.IsConfigured())

	plan.validateEffectiveUpdateState(state, config, &diags)
	require.True(t, diags.HasError())
	require.Contains(t, diags.Errors()[0].Summary(), "Missing Bedrock Settings")
}

func TestAIProviderUpdateRequestAPIKeyRotation(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		DisplayName:     types.StringValue("OpenAI"),
		Enabled:         types.BoolValue(true),
		BaseURL:         types.StringValue("https://api.openai.com"),
		APIKeyWOVersion: types.Int64Value(1),
	}

	t.Run("version unchanged", func(t *testing.T) {
		unchanged := state
		var diags diag.Diagnostics
		patch := unchanged.updateRequest(state, unchanged, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.Nil(t, patch.APIKeys)
	})

	t.Run("version bumped", func(t *testing.T) {
		plan := state
		plan.APIKeyWOVersion = types.Int64Value(2)
		config := plan
		config.APIKeyWO = types.StringValue("sk-rotated")

		var diags diag.Diagnostics
		patch := plan.updateRequest(state, config, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, patch.APIKeys)
		require.Len(t, *patch.APIKeys, 1)
		require.Nil(t, (*patch.APIKeys)[0].ID)
		require.Equal(t, "sk-rotated", *(*patch.APIKeys)[0].APIKey)
	})

	t.Run("version removed", func(t *testing.T) {
		removed := state
		removed.APIKeyWOVersion = types.Int64Null()
		var diags diag.Diagnostics
		patch := removed.updateRequest(state, removed, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.Nil(t, patch.APIKeys)
	})

	t.Run("version bumped without key", func(t *testing.T) {
		bumpedNoKey := state
		bumpedNoKey.APIKeyWOVersion = types.Int64Value(2)
		var diags diag.Diagnostics
		patch := bumpedNoKey.updateRequest(state, bumpedNoKey, &diags)
		require.True(t, diags.HasError())
		require.Contains(t, diags.Errors()[0].Summary(), "Missing API Key")
		require.Nil(t, patch.APIKeys)
	})
}

func TestAIProviderUpdateRejectsBedrockCredentialBumpWithoutCredentials(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		Type:    types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		Enabled: types.BoolValue(true),
		BaseURL: types.StringValue("https://bedrock-runtime.us-east-1.amazonaws.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Value(1),
		}},
	}
	// config bumps the credential version but omits the write-only credentials.
	plan := AIProviderResourceModel{
		Type:    state.Type,
		Enabled: state.Enabled,
		BaseURL: state.BaseURL,
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Value(2),
		}},
	}
	config := plan

	var diags diag.Diagnostics
	_ = plan.updateRequest(state, config, &diags)
	require.True(t, diags.HasError())
	require.Contains(t, diags.Errors()[0].Summary(), "Missing Bedrock Credentials")
}

func TestAIProviderUpdateRotatesBedrockCredentials(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		Type:    types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		Enabled: types.BoolValue(true),
		BaseURL: types.StringValue("https://bedrock-runtime.us-east-1.amazonaws.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Value(1),
		}},
	}
	plan := AIProviderResourceModel{
		Type:    state.Type,
		Enabled: state.Enabled,
		BaseURL: state.BaseURL,
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Value(2),
		}},
	}
	// Distinct non-empty values so a swapped key/secret assignment fails.
	config := plan
	config.Settings = &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
		Region:               types.StringValue("us-east-1"),
		AccessKeyWO:          types.StringValue("AKIANEWKEY"),
		AccessKeySecretWO:    types.StringValue("newsecretvalue"),
		CredentialsWOVersion: types.Int64Value(2),
	}}

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.Settings)
	require.NotNil(t, patch.Settings.Bedrock)
	require.NotNil(t, patch.Settings.Bedrock.AccessKey)
	require.NotNil(t, patch.Settings.Bedrock.AccessKeySecret)
	require.Equal(t, "AKIANEWKEY", *patch.Settings.Bedrock.AccessKey)
	require.Equal(t, "newsecretvalue", *patch.Settings.Bedrock.AccessKeySecret)
}

func TestAIProviderUpdatePreservesBedrockCredentialsWhenVersionRemoved(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		Type:    types.StringValue(string(codersdk.AIProviderTypeBedrock)),
		Enabled: types.BoolValue(true),
		BaseURL: types.StringValue("https://bedrock-runtime.us-east-1.amazonaws.com"),
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Value(1),
		}},
	}
	// The operator removes credentials_wo_version with credentials absent. This
	// must mean "stop managing / preserve", not "demand credentials forever".
	plan := AIProviderResourceModel{
		Type:    state.Type,
		Enabled: state.Enabled,
		BaseURL: state.BaseURL,
		Settings: &AIProviderSettingsModel{Bedrock: &AIProviderBedrockSettingsModel{
			Region:               types.StringValue("us-east-1"),
			CredentialsWOVersion: types.Int64Null(),
		}},
	}
	config := plan

	var diags diag.Diagnostics
	patch := plan.updateRequest(state, config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.Settings)
	require.NotNil(t, patch.Settings.Bedrock)
	// Credentials are preserved server-side: no credential pointers are sent.
	require.Nil(t, patch.Settings.Bedrock.AccessKey)
	require.Nil(t, patch.Settings.Bedrock.AccessKeySecret)

	plan.validateEffectiveUpdateState(state, config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
}

func TestParseBedrockRegionFromBaseURL(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		baseURL string
		want    string
	}{
		"canonical":         {"https://bedrock-runtime.us-east-1.amazonaws.com", "us-east-1"},
		"trailing slash":    {"https://bedrock-runtime.eu-west-1.amazonaws.com/", "eu-west-1"},
		"mixed case host":   {"https://Bedrock-Runtime.US-WEST-2.amazonaws.com", "us-west-2"},
		"surrounding space": {"  https://bedrock-runtime.ap-south-1.amazonaws.com  ", "ap-south-1"},
		"trailing path":     {"https://bedrock-runtime.us-east-1.amazonaws.com/foo", ""},
		"query string":      {"https://bedrock-runtime.us-east-1.amazonaws.com?x=1", ""},
		"port":              {"https://bedrock-runtime.us-east-1.amazonaws.com:443", ""},
		"non-bedrock host":  {"https://api.openai.com", ""},
		"empty":             {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, parseBedrockRegionFromBaseURL(tc.baseURL))
		})
	}
}
