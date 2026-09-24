package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func testAIModelPriceConfig(url, body string) string {
	return `provider "coderd" {
  url   = "` + url + `"
  token = "test-token"
}

` + body
}

func TestAccAIModelPriceResourceValidateConfig(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for name, tc := range map[string]struct {
		body      string
		wantError string
	}{
		"no prices": {
			body: `resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = "claude-sonnet-4-5"
}
`,
			wantError: `Missing Attribute Configuration`,
		},
		"negative price": {
			body: `resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = "claude-sonnet-4-5"
  input_price   = -1
}
`,
			wantError: `must be at least 0`,
		},
		"openai-compat provider type": {
			body: `resource "coderd_ai_model_price" "test" {
  provider_type = "openai-compat"
  model         = "my-model"
  input_price   = 100
}
`,
			wantError: `value must be one of`,
		},
		"empty model": {
			body: `resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = ""
  input_price   = 100
}
`,
			wantError: `string length must be at least 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      testAIModelPriceConfig("http://127.0.0.1", tc.body),
						ExpectError: regexp.MustCompile(tc.wantError),
					},
				},
			})
		})
	}
}

func TestAccAIModelPriceResourceDeferral(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for name, tc := range map[string]struct {
		body      string
		variables config.Variables
	}{
		"terraform_data outputs": {
			body: `resource "terraform_data" "provider_type" {
  input = "anthropic"
}

resource "terraform_data" "model" {
  input = "claude-sonnet-4-5"
}

resource "terraform_data" "price" {
  input = 3000000
}

resource "coderd_ai_model_price" "test" {
  provider_type = terraform_data.provider_type.output
  model         = terraform_data.model.output
  input_price   = terraform_data.price.output
}
`,
		},
		"required variables": {
			body: `variable "provider_type" {
  type = string
}

variable "model" {
  type = string
}

variable "input_price" {
  type = number
}

resource "coderd_ai_model_price" "test" {
  provider_type = var.provider_type
  model         = var.model
  input_price   = var.input_price
}
`,
			variables: config.Variables{
				"provider_type": config.StringVariable("anthropic"),
				"model":         config.StringVariable("claude-sonnet-4-5"),
				"input_price":   config.IntegerVariable(3000000),
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
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:             testAIModelPriceConfig(srv.URL, tc.body),
						ConfigVariables:    tc.variables,
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					},
				},
			})
		})
	}
}

func TestAIModelPriceImportState(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	r := &AIModelPriceResource{}
	schemaResp := &fwresource.SchemaResponse{}
	r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
	require.False(t, schemaResp.Diagnostics.HasError())

	for name, tc := range map[string]struct {
		id               string
		wantProviderType string
		wantModel        string
		wantError        bool
	}{
		"simple":             {id: "anthropic/claude-sonnet-4-5", wantProviderType: "anthropic", wantModel: "claude-sonnet-4-5"},
		"model with a slash": {id: "openrouter/anthropic/claude-sonnet-4.5", wantProviderType: "openrouter", wantModel: "anthropic/claude-sonnet-4.5"},
		"no separator":       {id: "anthropic", wantError: true},
		"empty provider":     {id: "/claude-sonnet-4-5", wantError: true},
		"empty model":        {id: "anthropic/", wantError: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resp := &fwresource.ImportStateResponse{
				State: tfsdk.State{
					Schema: schemaResp.Schema,
					Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
				},
			}
			r.ImportState(ctx, fwresource.ImportStateRequest{ID: tc.id}, resp)
			if tc.wantError {
				require.True(t, resp.Diagnostics.HasError())
				return
			}
			require.False(t, resp.Diagnostics.HasError(), resp.Diagnostics)

			var state AIModelPriceResourceModel
			require.False(t, resp.State.Get(ctx, &state).HasError())
			require.Equal(t, types.StringValue(tc.id), state.ID)
			require.Equal(t, types.StringValue(tc.wantProviderType), state.ProviderType)
			require.Equal(t, types.StringValue(tc.wantModel), state.Model)
		})
	}
}

func TestAccAIModelPriceResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ai_model_price_acc", integration.UseLicense)
	exp := codersdk.NewExperimentalClient(client)

	entitlements, err := client.Entitlements(ctx)
	require.NoError(t, err)
	if !entitlements.Features[codersdk.FeatureAIBridge].Enabled {
		t.Skip("The license is not entitled to AI Gateway.")
	}

	// Price a model that Coder ships a default price for, so Read must pick the
	// custom price over the default price.
	defaults, err := exp.ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
		Provider: string(codersdk.AIProviderTypeAnthropic),
		Source:   codersdk.AIModelPriceSourceFilterDefault,
	})
	require.NoError(t, err)
	if len(defaults) == 0 {
		t.Skip("The Coder server has no default anthropic model prices.")
	}
	model := defaults[0].Model
	const slashModel = "vendor/tf-acc-model"

	cfg := func(input, output int64, cacheRead string, withSlash bool) string {
		body := fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_ai_model_price" "test" {
  provider_type    = "anthropic"
  model            = %q
  input_price      = %d
  output_price     = %d
  cache_read_price = %s
}
`, client.URL.String(), client.SessionToken(), model, input, output, cacheRead)
		if withSlash {
			body += fmt.Sprintf(`
resource "coderd_ai_model_price" "slash" {
  provider_type = "openrouter"
  model         = %q
  input_price   = 100
}
`, slashModel)
		}
		return body
	}

	customPrice := func(provider, model string) (*codersdk.AIModelPrice, error) {
		prices, err := exp.ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
			Provider: provider,
			Model:    model,
			Source:   codersdk.AIModelPriceSourceFilterCustom,
		})
		if err != nil {
			return nil, err
		}
		if len(prices) == 0 {
			return nil, nil
		}
		return &prices[0], nil
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, key := range [][2]string{{"anthropic", model}, {"openrouter", slashModel}} {
				price, err := customPrice(key[0], key[1])
				if err != nil {
					return err
				}
				if price == nil {
					return fmt.Errorf("custom price for %s/%s was deleted, but destroy must only remove it from the state", key[0], key[1])
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(100, 200, "null", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_ai_model_price.test", "id", "anthropic/"+model),
					resource.TestCheckResourceAttr("coderd_ai_model_price.test", "input_price", "100"),
					resource.TestCheckResourceAttr("coderd_ai_model_price.test", "output_price", "200"),
					resource.TestCheckNoResourceAttr("coderd_ai_model_price.test", "cache_read_price"),
					resource.TestCheckNoResourceAttr("coderd_ai_model_price.test", "cache_write_price"),
				),
			},
			{
				Config: cfg(150, 250, "0", false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_ai_model_price.test", "input_price", "150"),
					resource.TestCheckResourceAttr("coderd_ai_model_price.test", "cache_read_price", "0"),
					func(*terraform.State) error {
						price, err := customPrice("anthropic", model)
						if err != nil {
							return err
						}
						if price == nil {
							return fmt.Errorf("custom price for anthropic/%s not found", model)
						}
						if price.CacheReadPrice == nil || *price.CacheReadPrice != 0 {
							return fmt.Errorf("expected cache_read_price 0, got %v", price.CacheReadPrice)
						}
						if price.CacheWritePrice != nil {
							return fmt.Errorf("expected a null cache_write_price, got %d", *price.CacheWritePrice)
						}
						return nil
					},
				),
			},
			{
				PreConfig: func() {
					drifted := int64(999)
					require.NoError(t, exp.UpsertAIModelPrices(ctx, codersdk.UpsertAIModelPricesRequest{
						Prices: []codersdk.AIModelPriceUpsert{{
							Provider:   "anthropic",
							Model:      model,
							InputPrice: &drifted,
						}},
					}))
				},
				Config: cfg(150, 250, "0", false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_ai_model_price.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					price, err := customPrice("anthropic", model)
					if err != nil {
						return err
					}
					if price == nil || price.InputPrice == nil || *price.InputPrice != 150 {
						return fmt.Errorf("expected the drifted price to be restored to 150, got %+v", price)
					}
					return nil
				},
			},
			{
				ResourceName:      "coderd_ai_model_price.test",
				ImportState:       true,
				ImportStateId:     "anthropic/" + model,
				ImportStateVerify: true,
			},
			{
				Config: cfg(150, 250, "0", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_ai_model_price.slash", "id", "openrouter/"+slashModel),
					resource.TestCheckResourceAttr("coderd_ai_model_price.slash", "model", slashModel),
				),
			},
			{
				ResourceName:      "coderd_ai_model_price.slash",
				ImportState:       true,
				ImportStateId:     "openrouter/" + slashModel,
				ImportStateVerify: true,
			},
		},
	})
}
