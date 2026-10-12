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
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func testAIGroupBudgetConfig(url, body string) string {
	return `provider "coderd" {
  url   = "` + url + `"
  token = "test-token"
}

` + body
}

func TestAccAIGroupBudgetResourceValidateConfig(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for name, tc := range map[string]struct {
		body      string
		wantError string
	}{
		"negative spend limit": {
			body: `resource "coderd_ai_group_budget" "test" {
  group_id    = "00000000-0000-0000-0000-000000000003"
  spend_limit = -1
}
`,
			wantError: `must be between 0 and 1000000000000`,
		},
		"spend limit above the maximum": {
			body: `resource "coderd_ai_group_budget" "test" {
  group_id    = "00000000-0000-0000-0000-000000000003"
  spend_limit = 1000000000001
}
`,
			wantError: `must be between 0 and 1000000000000`,
		},
		"invalid group ID": {
			body: `resource "coderd_ai_group_budget" "test" {
  group_id    = "not-a-uuid"
  spend_limit = 100
}
`,
			wantError: `Invalid UUID`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      testAIGroupBudgetConfig("http://127.0.0.1", tc.body),
						ExpectError: regexp.MustCompile(tc.wantError),
					},
				},
			})
		})
	}
}

func TestAccAIGroupBudgetResourceModifyPlan(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	const groupID = "00000000-0000-0000-0000-000000000003"
	const knownConfig = `resource "coderd_ai_group_budget" "test" {
  group_id    = "` + groupID + `"
  spend_limit = 50000000
}
`
	for name, tc := range map[string]struct {
		status    int
		body      string
		config    string
		wantError string
	}{
		"different budget conflicts": {
			status:    http.StatusOK,
			body:      `{"group_id":"` + groupID + `","spend_limit_micros":1}`,
			config:    knownConfig,
			wantError: `AI Group Budget Already Exists`,
		},
		"equal budget is adopted": {
			status: http.StatusOK,
			body:   `{"group_id":"` + groupID + `","spend_limit_micros":50000000}`,
			config: knownConfig,
		},
		"no budget": {
			status: http.StatusNotFound,
			body:   `{"message":"Resource not found or you do not have access to this resource"}`,
			config: knownConfig,
		},
		"unknown spend limit defers the conflict check": {
			status: http.StatusOK,
			body:   `{"group_id":"` + groupID + `","spend_limit_micros":1}`,
			config: `resource "terraform_data" "limit" {
  input = 50000000
}

resource "coderd_ai_group_budget" "test" {
  group_id    = "` + groupID + `"
  spend_limit = terraform_data.limit.output
}
`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The mock server answers the user and entitlements requests from Configure().
			mock := newMockServer(nil)
			defer mock.Close()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v2/groups/"+groupID+"/ai/budget" {
					mock.Config.Handler.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			step := resource.TestStep{
				Config:             testAIGroupBudgetConfig(srv.URL, tc.config),
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

func TestAccAIGroupBudgetResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ai_group_budget_acc", integration.UseLicense)
	me, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	org, err := client.Organization(ctx, me.OrganizationIDs[0])
	require.NoError(t, err)

	createGroup := func(name string) codersdk.Group {
		group, err := client.CreateGroup(ctx, org.ID, codersdk.CreateGroupRequest{Name: name})
		require.NoError(t, err)
		return group
	}
	group := createGroup("tf-acc-budget")
	conflictGroup := createGroup("tf-acc-budget-conflict")

	cfg := func(spendLimit int64) string {
		return fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_ai_group_budget" "test" {
  group_id    = %q
  spend_limit = %d
}
`, client.URL.String(), client.SessionToken(), group.ID, spendLimit)
	}
	conflictCfg := func(spendLimit int64) string {
		return fmt.Sprintf(`
resource "coderd_ai_group_budget" "conflict" {
  group_id    = %q
  spend_limit = %d
}
`, conflictGroup.ID, spendLimit)
	}
	upsert := func(groupID uuid.UUID, spendLimit int64) {
		_, err := client.UpsertGroupAIBudget(ctx, groupID, codersdk.UpsertGroupAIBudgetRequest{SpendLimitMicros: spendLimit})
		require.NoError(t, err)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, id := range []uuid.UUID{group.ID, conflictGroup.ID} {
				_, err := client.GroupAIBudget(ctx, id)
				if !isNotFound(err) {
					return fmt.Errorf("expected the AI budget for group %s to be deleted, got error: %v", id, err)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(50000000),
				Check:  resource.TestCheckResourceAttr("coderd_ai_group_budget.test", "spend_limit", "50000000"),
			},
			{
				Config: cfg(0),
				Check:  resource.TestCheckResourceAttr("coderd_ai_group_budget.test", "spend_limit", "0"),
			},
			{
				ResourceName:                         "coderd_ai_group_budget.test",
				ImportState:                          true,
				ImportStateId:                        group.ID.String(),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "group_id",
			},
			{
				ResourceName:                         "coderd_ai_group_budget.test",
				ImportState:                          true,
				ImportStateId:                        org.Name + "/" + group.Name,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "group_id",
			},
			{
				// Terraform recreates a budget that was deleted outside of Terraform.
				PreConfig: func() { require.NoError(t, client.DeleteGroupAIBudget(ctx, group.ID)) },
				Config:    cfg(0),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_ai_group_budget.test", plancheck.ResourceActionCreate),
					},
				},
			},
			{
				PreConfig:   func() { upsert(conflictGroup.ID, 1000000) },
				Config:      cfg(0) + conflictCfg(2000000),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("AI Group Budget Already Exists"),
			},
			{
				// Terraform adopts an existing budget that equals the config.
				Config: cfg(0) + conflictCfg(1000000),
			},
		},
	})
}
