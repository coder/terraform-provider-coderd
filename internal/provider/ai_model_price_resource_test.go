package provider

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
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
		"openai-compat provider type": {
			body: `resource "coderd_ai_model_price" "test" {
  provider_type = "openai-compat"
  model         = "my-model"
  input_price   = 100
}
`,
			wantError: `value must be one of`,
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

func TestAccAIModelPriceResourceModifyPlan(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	const knownConfig = `resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = "claude-sonnet-4-5"
  input_price   = 3000000
}
`
	const existingPrice = `[{"provider":"anthropic","model":"claude-sonnet-4-5","input_price":1,"source":"custom"}]`
	for name, tc := range map[string]struct {
		status    int
		body      string
		config    string
		wantError string
	}{
		"known values conflict": {
			status:    http.StatusOK,
			body:      existingPrice,
			config:    knownConfig,
			wantError: `AI Model Price Already Exists`,
		},
		"unknown model defers the conflict check": {
			status: http.StatusOK,
			body:   existingPrice,
			config: `resource "terraform_data" "model" {
  input = "claude-sonnet-4-5"
}

resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = terraform_data.model.output
  input_price   = 3000000
}
`,
		},
		"unknown price defers the conflict check": {
			status: http.StatusOK,
			body:   existingPrice,
			config: `resource "terraform_data" "price" {
  input = 3000000
}

resource "coderd_ai_model_price" "test" {
  provider_type = "anthropic"
  model         = "claude-sonnet-4-5"
  input_price   = terraform_data.price.output
}
`,
		},
		"unsupported Coder version": {
			status:    http.StatusNotFound,
			body:      `{"message":"Route not found."}`,
			config:    knownConfig,
			wantError: `Unsupported Coder Version`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The mock server answers the user and entitlements requests from Configure().
			mock := newMockServer(nil)
			defer mock.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/experimental/ai/model-prices" {
					mock.Config.Handler.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			step := resource.TestStep{
				Config:             testAIModelPriceConfig(srv.URL, tc.config),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			}
			if tc.wantError != "" {
				step.ExpectError = regexp.MustCompile(tc.wantError)
			}
			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps:                    []resource.TestStep{step},
			})
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

	// Price a model that has a default price, so the conflict check and Read
	// must ignore the default price.
	defaults, err := exp.ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
		Provider: string(codersdk.AIProviderTypeAnthropic),
		Source:   codersdk.AIModelPriceSourceFilterDefault,
	})
	require.NoError(t, err)
	require.NotEmpty(t, defaults)
	model := defaults[0].Model
	const slashModel = "vendor/tf-acc-model"
	const conflictModel = "tf-acc-conflict-model"

	cfg := func(input, output int64, cacheRead string) string {
		return fmt.Sprintf(`
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

resource "coderd_ai_model_price" "slash" {
  provider_type = "openrouter"
  model         = %q
  input_price   = 100
}
`, client.URL.String(), client.SessionToken(), model, input, output, cacheRead, slashModel)
	}
	conflictCfg := func(input int64) string {
		return fmt.Sprintf(`
resource "coderd_ai_model_price" "conflict" {
  provider_type = "openai"
  model         = %q
  input_price   = %d
}
`, conflictModel, input)
	}
	upsert := func(provider, name string, input int64) {
		require.NoError(t, exp.UpsertAIModelPrices(ctx, codersdk.UpsertAIModelPricesRequest{
			Prices: []codersdk.AIModelPriceUpsert{{
				Provider:   provider,
				Model:      name,
				InputPrice: &input,
			}},
		}))
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			prices, err := exp.ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
				Provider: "anthropic",
				Model:    model,
				Source:   codersdk.AIModelPriceSourceFilterCustom,
			})
			if err != nil {
				return err
			}
			if len(prices) == 0 {
				return fmt.Errorf("custom price for anthropic/%s was deleted, but destroy must only remove it from the state", model)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(100, 200, "null"),
				Check:  resource.TestCheckResourceAttr("coderd_ai_model_price.slash", "id", "openrouter/"+slashModel),
			},
			{
				Config: cfg(150, 250, "0"),
			},
			{
				PreConfig: func() { upsert("anthropic", model, 999) },
				Config:    cfg(150, 250, "0"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_ai_model_price.test", plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				ResourceName:      "coderd_ai_model_price.test",
				ImportState:       true,
				ImportStateId:     "anthropic/" + model,
				ImportStateVerify: true,
			},
			{
				ResourceName:      "coderd_ai_model_price.slash",
				ImportState:       true,
				ImportStateId:     "openrouter/" + slashModel,
				ImportStateVerify: true,
			},
			{
				PreConfig:   func() { upsert("openai", conflictModel, 1000000) },
				Config:      cfg(150, 250, "0") + conflictCfg(2000000),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("AI Model Price Already Exists"),
			},
			{
				// Terraform adopts an existing custom price that equals the config.
				Config: cfg(150, 250, "0") + conflictCfg(1000000),
			},
		},
	})
}
