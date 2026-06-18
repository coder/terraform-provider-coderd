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
	}
	cfg2 := cfg1
	cfg2.OpenAIKey = "sk-test-primary-111111"
	cfg2.OpenAIKeyVersion = 2

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
		},
	})
}

type testAccAIProviderResourceConfig struct {
	URL              string
	Token            string
	OpenAIKey        string
	OpenAIKeyVersion int
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
  base_url     = "https://bedrock-runtime.us-east-1.amazonaws.com"

  settings = {
    bedrock = {
      region           = "us-east-1"
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
	config := plan
	config.DisplayName = types.StringNull()
	config.Settings.Bedrock.Region = types.StringNull()

	var diags diag.Diagnostics
	req := plan.createRequest(config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Empty(t, req.APIKeys)
	require.NotNil(t, req.Settings.Bedrock)
	require.Equal(t, "us-east-1", req.Settings.Bedrock.Region)
	require.Nil(t, req.Settings.Bedrock.AccessKey)
	require.Nil(t, req.Settings.Bedrock.AccessKeySecret)
}

func TestAIProviderUpdateRequestAPIKeyRotation(t *testing.T) {
	t.Parallel()

	state := AIProviderResourceModel{
		DisplayName:     types.StringValue("OpenAI"),
		Enabled:         types.BoolValue(true),
		BaseURL:         types.StringValue("https://api.openai.com"),
		APIKeyWOVersion: types.Int64Value(1),
	}

	// Version unchanged: keys are left untouched (no api_keys in the patch).
	unchanged := state
	var diags diag.Diagnostics
	patch := unchanged.updateRequest(state, unchanged, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.Nil(t, patch.APIKeys)

	// Version bumped: the new plaintext is sent as a single insert mutation,
	// which replaces the old key (its ID is absent from the list).
	plan := state
	plan.APIKeyWOVersion = types.Int64Value(2)
	config := plan
	config.APIKeyWO = types.StringValue("sk-rotated")

	diags = diag.Diagnostics{}
	patch = plan.updateRequest(state, config, &diags)
	require.False(t, diags.HasError(), diags.Errors())
	require.NotNil(t, patch.APIKeys)
	require.Len(t, *patch.APIKeys, 1)
	require.Nil(t, (*patch.APIKeys)[0].ID)
	require.Equal(t, "sk-rotated", *(*patch.APIKeys)[0].APIKey)
}
