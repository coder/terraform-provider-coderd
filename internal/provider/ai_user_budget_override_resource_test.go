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

func testAIUserBudgetOverrideConfig(url, body string) string {
	return `provider "coderd" {
  url   = "` + url + `"
  token = "test-token"
}

` + body
}

func TestAccAIUserBudgetOverrideResourceValidateConfig(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for name, tc := range map[string]struct {
		body      string
		wantError string
	}{
		"negative spend limit": {
			body: `resource "coderd_ai_user_budget_override" "test" {
  user_id     = "00000000-0000-0000-0000-000000000003"
  group_id    = "00000000-0000-0000-0000-000000000004"
  spend_limit = -1
}
`,
			wantError: `must be between 0 and 1000000000000`,
		},
		"invalid user ID": {
			body: `resource "coderd_ai_user_budget_override" "test" {
  user_id     = "not-a-uuid"
  group_id    = "00000000-0000-0000-0000-000000000004"
  spend_limit = 100
}
`,
			wantError: `Invalid UUID`,
		},
		"invalid group ID": {
			body: `resource "coderd_ai_user_budget_override" "test" {
  user_id     = "00000000-0000-0000-0000-000000000003"
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
						Config:      testAIUserBudgetOverrideConfig("http://127.0.0.1", tc.body),
						ExpectError: regexp.MustCompile(tc.wantError),
					},
				},
			})
		})
	}
}

func TestAccAIUserBudgetOverrideResourceModifyPlan(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	const userID = "00000000-0000-0000-0000-000000000003"
	const groupID = "00000000-0000-0000-0000-000000000004"
	const knownConfig = `resource "coderd_ai_user_budget_override" "test" {
  user_id     = "` + userID + `"
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
		"different spend limit conflicts": {
			status:    http.StatusOK,
			body:      `{"user_id":"` + userID + `","group_id":"` + groupID + `","spend_limit_micros":1}`,
			config:    knownConfig,
			wantError: `AI User Budget Override Already Exists`,
		},
		"different group conflicts": {
			status:    http.StatusOK,
			body:      `{"user_id":"` + userID + `","group_id":"00000000-0000-0000-0000-000000000005","spend_limit_micros":50000000}`,
			config:    knownConfig,
			wantError: `AI User Budget Override Already Exists`,
		},
		"equal override is adopted": {
			status: http.StatusOK,
			body:   `{"user_id":"` + userID + `","group_id":"` + groupID + `","spend_limit_micros":50000000}`,
			config: knownConfig,
		},
		"no override": {
			status: http.StatusNotFound,
			body:   `{"message":"Resource not found or you do not have access to this resource"}`,
			config: knownConfig,
		},
		"unknown group defers the conflict check": {
			status: http.StatusOK,
			body:   `{"user_id":"` + userID + `","group_id":"` + groupID + `","spend_limit_micros":1}`,
			config: `resource "terraform_data" "group" {
  input = "` + groupID + `"
}

resource "coderd_ai_user_budget_override" "test" {
  user_id     = "` + userID + `"
  group_id    = terraform_data.group.output
  spend_limit = 50000000
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
				if r.URL.Path != "/api/v2/users/"+userID+"/ai/budget/override" {
					mock.Config.Handler.ServeHTTP(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			step := resource.TestStep{
				Config:             testAIUserBudgetOverrideConfig(srv.URL, tc.config),
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

func TestAccAIUserBudgetOverrideResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ai_user_budget_override_acc", integration.UseLicense)
	me, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	orgID := me.OrganizationIDs[0]

	createUser := func(username string) codersdk.User {
		user, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
			Email:           username + "@coder.com",
			Username:        username,
			Password:        "SomeSecurePassword!",
			UserLoginType:   codersdk.LoginTypePassword,
			OrganizationIDs: []uuid.UUID{orgID},
		})
		require.NoError(t, err)
		return user
	}
	createGroup := func(name string, members ...codersdk.User) codersdk.Group {
		group, err := client.CreateGroup(ctx, orgID, codersdk.CreateGroupRequest{Name: name})
		require.NoError(t, err)
		var memberIDs []string
		for _, member := range members {
			memberIDs = append(memberIDs, member.ID.String())
		}
		group, err = client.PatchGroup(ctx, group.ID, codersdk.PatchGroupRequest{AddUsers: memberIDs})
		require.NoError(t, err)
		return group
	}
	user := createUser("tf-acc-override")
	conflictUser := createUser("tf-acc-override-conflict")
	group := createGroup("tf-acc-override", user, conflictUser)
	otherGroup := createGroup("tf-acc-override-other", user)
	nonMemberGroup := createGroup("tf-acc-override-non-member", conflictUser)

	cfg := func(groupID uuid.UUID, spendLimit int64) string {
		return fmt.Sprintf(`
provider "coderd" {
  url   = %q
  token = %q
}

resource "coderd_ai_user_budget_override" "test" {
  user_id     = %q
  group_id    = %q
  spend_limit = %d
}
`, client.URL.String(), client.SessionToken(), user.ID, groupID, spendLimit)
	}
	conflictCfg := func(spendLimit int64) string {
		return fmt.Sprintf(`
resource "coderd_ai_user_budget_override" "conflict" {
  user_id     = %q
  group_id    = %q
  spend_limit = %d
}
`, conflictUser.ID, group.ID, spendLimit)
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			for _, id := range []uuid.UUID{user.ID, conflictUser.ID} {
				_, err := client.UserAIBudgetOverride(ctx, id)
				if !isNotFound(err) {
					return fmt.Errorf("expected the AI budget override for user %s to be deleted, got error: %v", id, err)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: cfg(group.ID, 50000000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_ai_user_budget_override.test", "group_id", group.ID.String()),
					resource.TestCheckResourceAttr("coderd_ai_user_budget_override.test", "spend_limit", "50000000"),
				),
			},
			{
				Config: cfg(otherGroup.ID, 0),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_ai_user_budget_override.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_ai_user_budget_override.test", "group_id", otherGroup.ID.String()),
					resource.TestCheckResourceAttr("coderd_ai_user_budget_override.test", "spend_limit", "0"),
				),
			},
			{
				ResourceName:                         "coderd_ai_user_budget_override.test",
				ImportState:                          true,
				ImportStateId:                        user.ID.String(),
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "user_id",
			},
			{
				ResourceName:                         "coderd_ai_user_budget_override.test",
				ImportState:                          true,
				ImportStateId:                        user.Username,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "user_id",
			},
			{
				Config:      cfg(nonMemberGroup.ID, 0),
				ExpectError: regexp.MustCompile("User is not a member of the referenced group"),
			},
			{
				// Terraform recreates an override that was deleted outside of Terraform.
				PreConfig: func() { require.NoError(t, client.DeleteUserAIBudgetOverride(ctx, user.ID)) },
				Config:    cfg(otherGroup.ID, 0),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_ai_user_budget_override.test", plancheck.ResourceActionCreate),
					},
				},
			},
			{
				PreConfig: func() {
					_, err := client.UpsertUserAIBudgetOverride(ctx, conflictUser.ID, codersdk.UpsertUserAIBudgetOverrideRequest{
						GroupID:          group.ID,
						SpendLimitMicros: 1000000,
					})
					require.NoError(t, err)
				},
				Config:      cfg(otherGroup.ID, 0) + conflictCfg(2000000),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("AI User Budget Override Already Exists"),
			},
			{
				// Terraform adopts an existing override that equals the config.
				Config: cfg(otherGroup.ID, 0) + conflictCfg(1000000),
			},
		},
	})
}
