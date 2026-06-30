package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration performs an integration test against an ephemeral Coder deployment.
// For each directory containing a `main.tf` under `/integration`, performs the following:
// - Creates a temporary Coder instance running in Docker
// - Runs the `main.tf` specified in the given test directory against the Coder deployment
// - Asserts the state of the deployment via `codersdk`.
func TestIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "1" {
		t.Skip("Skipping integration tests during tf acceptance tests")
	}
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	timeoutStr := os.Getenv("TIMEOUT_MINS")
	if timeoutStr == "" {
		timeoutStr = "10"
	}
	timeoutMins, err := strconv.Atoi(timeoutStr)
	require.NoError(t, err, "invalid value specified for timeout")
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(timeoutMins)*time.Minute)
	t.Cleanup(cancel)

	tfrcPath := setupProvider(t)

	for _, tt := range []struct {
		name    string
		preF    func(testing.TB, *codersdk.Client)
		assertF func(testing.TB, *codersdk.Client)
	}{
		{
			name: "user-test",
			preF: func(t testing.TB, c *codersdk.Client) {
				me, err := c.User(ctx, codersdk.Me)
				assert.NoError(t, err)
				user1, err := c.CreateUser(ctx, codersdk.CreateUserRequest{
					Email:          "test2@coder.com",
					Username:       "ethan",
					Password:       "SomeSecurePassword!",
					UserLoginType:  "password",
					DisableLogin:   false,
					OrganizationID: me.OrganizationIDs[0],
				})
				assert.NoError(t, err)
				group, err := c.CreateGroup(ctx, me.OrganizationIDs[0], codersdk.CreateGroupRequest{
					Name:           "bosses",
					QuotaAllowance: 200,
				})
				assert.NoError(t, err)
				_, err = c.PatchGroup(ctx, group.ID, codersdk.PatchGroupRequest{
					AddUsers: []string{user1.ID.String()},
				})
				assert.NoError(t, err)
			},
			assertF: func(t testing.TB, c *codersdk.Client) {
				// Check user fields.
				user, err := c.User(ctx, "dean")
				assert.NoError(t, err)
				assert.Equal(t, "dean", user.Username)
				assert.Equal(t, "Dean Coolguy", user.Name)
				assert.Equal(t, "test@coder.com", user.Email)
				roles := make([]string, len(user.Roles))
				for i, role := range user.Roles {
					roles[i] = role.Name
				}
				assert.ElementsMatch(t, []string{"owner", "template-admin"}, roles)
				assert.Equal(t, codersdk.LoginTypePassword, user.LoginType)
				assert.Contains(t, []codersdk.UserStatus{codersdk.UserStatusActive, codersdk.UserStatusDormant}, user.Status)

				// Test password.
				newClient := codersdk.New(c.URL)
				res, err := newClient.LoginWithPassword(ctx, codersdk.LoginWithPasswordRequest{
					Email:    "test@coder.com",
					Password: "SomeSecurePassword!",
				})
				assert.NoError(t, err)
				newClient.SetSessionToken(res.SessionToken)
				user, err = newClient.User(ctx, codersdk.Me)
				assert.NoError(t, err)
				assert.Equal(t, "dean", user.Username)

				// Check group
				defaultOrg, err := c.OrganizationByName(ctx, "coder")
				assert.NoError(t, err)
				group, err := c.GroupByOrgAndName(ctx, defaultOrg.ID, "employees")
				assert.NoError(t, err)
				assert.Len(t, group.Members, 3)
				assert.Equal(t, group.QuotaAllowance, 100)
			},
		},
		{
			name: "org-group-sync-test",
			preF: func(t testing.TB, c *codersdk.Client) {},
			assertF: func(t testing.TB, c *codersdk.Client) {
				org, err := c.OrganizationByName(ctx, "test-org-group-sync")
				assert.NoError(t, err)
				assert.Equal(t, "test-org-group-sync", org.Name)
				assert.Equal(t, "Test Organization for Group Sync", org.DisplayName)

				testGroup, err := c.GroupByOrgAndName(ctx, org.ID, "test-group")
				assert.NoError(t, err)
				assert.Equal(t, "test-group", testGroup.Name)
				assert.Equal(t, "Test Group", testGroup.DisplayName)
				assert.Equal(t, 50, testGroup.QuotaAllowance)

				adminGroup, err := c.GroupByOrgAndName(ctx, org.ID, "admin-group")
				assert.NoError(t, err)
				assert.Equal(t, "admin-group", adminGroup.Name)
				assert.Equal(t, "Admin Group", adminGroup.DisplayName)
				assert.Equal(t, 100, adminGroup.QuotaAllowance)

				// Verify group sync settings
				groupSync, err := c.GroupIDPSyncSettings(ctx, org.ID.String())
				assert.NoError(t, err)
				assert.Equal(t, "groups", groupSync.Field)
				assert.NotNil(t, groupSync.RegexFilter)
				assert.Equal(t, "test_.*|admin_.*", groupSync.RegexFilter.String())
				assert.False(t, groupSync.AutoCreateMissing)

				assert.Contains(t, groupSync.Mapping, "test_developers")
				assert.Contains(t, groupSync.Mapping, "admin_users")
				assert.Contains(t, groupSync.Mapping, "mixed_group")

				assert.Contains(t, groupSync.Mapping["test_developers"], testGroup.ID)
				assert.Contains(t, groupSync.Mapping["admin_users"], adminGroup.ID)
				assert.Contains(t, groupSync.Mapping["mixed_group"], testGroup.ID)
				assert.Contains(t, groupSync.Mapping["mixed_group"], adminGroup.ID)
			},
		},
		{
			name: "template-port-share-test",
			preF: func(t testing.TB, c *codersdk.Client) {},
			assertF: func(t testing.TB, c *codersdk.Client) {
				templates, err := c.Templates(ctx, codersdk.TemplateFilter{})
				require.NoError(t, err)
				require.Len(t, templates, 1)
				require.Equal(t, "port-share-test", templates[0].Name)
				require.Equal(t, codersdk.WorkspaceAgentPortShareLevelOrganization, templates[0].MaxPortShareLevel)
			},
		},
		{
			name: "template-test",
			preF: func(t testing.TB, c *codersdk.Client) {},
			assertF: func(t testing.TB, c *codersdk.Client) {
				defaultOrg, err := c.OrganizationByName(ctx, "coder")
				assert.NoError(t, err)
				user, err := c.User(ctx, "ethan")
				require.NoError(t, err)

				// Check template metadata
				templates, err := c.Templates(ctx, codersdk.TemplateFilter{})
				require.NoError(t, err)
				require.Len(t, templates, 1)
				require.Equal(t, "example-template", templates[0].Name)
				require.False(t, templates[0].AllowUserAutostart)
				require.False(t, templates[0].AllowUserAutostop)

				// Check versions
				versions, err := c.TemplateVersionsByTemplate(ctx, codersdk.TemplateVersionsByTemplateRequest{
					TemplateID: templates[0].ID,
				})
				require.NoError(t, err)
				require.Len(t, versions, 2)
				require.NotEmpty(t, versions[0].ID)
				require.Equal(t, templates[0].ID, *versions[0].TemplateID)
				require.Equal(t, templates[0].ActiveVersionID, versions[0].ID)

				// Check ACL
				acl, err := c.TemplateACL(ctx, templates[0].ID)
				require.NoError(t, err)
				require.Len(t, acl.Groups, 1)
				require.Equal(t, codersdk.TemplateRoleUse, acl.Groups[0].Role)
				require.Equal(t, defaultOrg.ID, acl.Groups[0].ID)
				require.Len(t, acl.Users, 1)
				require.Equal(t, codersdk.TemplateRoleAdmin, acl.Users[0].Role)
				require.Equal(t, user.ID, acl.Users[0].ID)
			},
		},
		{
			name: "agents-model-test",
			preF: func(t testing.TB, c *codersdk.Client) {},
			assertF: func(t testing.TB, c *codersdk.Client) {
				providers, err := c.AIProviders(ctx)
				require.NoError(t, err)
				require.Len(t, providers, 2)

				exp := codersdk.NewExperimentalClient(c)
				configs, err := exp.ListChatModelConfigs(ctx)
				require.NoError(t, err)

				// model -> {provider type, expected model_config JSON} (mirrors main.tf).
				want := map[string]struct{ provider, config string }{
					"claude-opus-4-8": {"anthropic", `{
						"max_output_tokens": 128000,
						"cost": {
							"input_price_per_million_tokens": "5",
							"output_price_per_million_tokens": "25",
							"cache_read_price_per_million_tokens": "0.5",
							"cache_write_price_per_million_tokens": "6.25"
						},
						"provider_options": {
							"anthropic": {
								"send_reasoning": true,
								"effort": "high"
							}
						}
					}`},
					"claude-sonnet-4-6": {"anthropic", `{
						"cost": {
							"input_price_per_million_tokens": "3",
							"output_price_per_million_tokens": "15"
						},
						"provider_options": {
							"anthropic": {
								"send_reasoning": true,
								"effort": "max",
								"web_search_enabled": true,
								"thinking": {
									"budget_tokens": 16000
								}
							}
						}
					}`},
					"gpt-5.5": {"openai", `{
						"cost": {
							"input_price_per_million_tokens": "2.5",
							"output_price_per_million_tokens": "15",
							"cache_read_price_per_million_tokens": "0.25"
						},
						"provider_options": {
							"openai": {
								"parallel_tool_calls": false,
								"reasoning_effort": "xhigh",
								"reasoning_summary": "detailed",
								"text_verbosity": "high",
								"web_search_enabled": true,
								"search_context_size": "medium"
							}
						}
					}`},
					"gpt-5.4-mini": {"openai", `{
						"provider_options": {
							"openai": {
								"reasoning_effort": "medium"
							}
						}
					}`},
				}
				require.Len(t, configs, len(want))

				var defaults []string
				for _, m := range configs {
					if m.IsDefault {
						defaults = append(defaults, m.Model)
					}
					w, ok := want[m.Model]
					require.True(t, ok, "unexpected model %s", m.Model)
					assert.Equal(t, w.provider, m.Provider)
					require.NotNil(t, m.ModelConfig)
					got, err := json.Marshal(m.ModelConfig)
					require.NoError(t, err)
					var wantConfig, gotConfig any
					require.NoError(t, json.Unmarshal([]byte(w.config), &wantConfig))
					require.NoError(t, json.Unmarshal(got, &gotConfig))
					if diff := cmp.Diff(wantConfig, gotConfig); diff != "" {
						t.Errorf("model_config for %s mismatch (-want +got):\n%s", m.Model, diff)
					}
				}
				// coderd_default_agents_model.default points at claude_sonnet, which
				// demotes the auto-promoted claude_opus, so Sonnet is the sole default.
				assert.Equal(t, []string{"claude-sonnet-4-6"}, defaults)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := StartCoder(ctx, t, tt.name, UseLicense)
			wd, err := os.Getwd()
			require.NoError(t, err)
			srcDir := filepath.Join(wd, tt.name)
			// Delete all .tfstate files
			err = filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
				if filepath.Ext(path) == ".tfstate" {
					return os.Remove(path)
				}
				return nil
			})
			require.NoError(t, err)
			tfCmd := exec.CommandContext(ctx, "terraform", "-chdir="+srcDir, "apply", "-auto-approve")
			tfCmd.Env = append(tfCmd.Env, "TF_CLI_CONFIG_FILE="+tfrcPath)
			tfCmd.Env = append(tfCmd.Env, "CODER_URL="+client.URL.String())
			tfCmd.Env = append(tfCmd.Env, "CODER_SESSION_TOKEN="+client.SessionToken())
			var buf bytes.Buffer
			tfCmd.Stdout = &buf
			tfCmd.Stderr = &buf
			tt.preF(t, client)
			if err := tfCmd.Run(); !assert.NoError(t, err) {
				t.Log(buf.String())
				t.FailNow()
			}
			tt.assertF(t, client)
		})
	}
}

func setupProvider(t testing.TB) string {
	// Ensure the binary is built
	binPath, err := filepath.Abs("../terraform-provider-coderd")
	require.NoError(t, err)
	if _, err := os.Stat(binPath); os.IsNotExist(err) {
		t.Fatalf("not found: %q - please build the provider first", binPath)
	}
	// Create a terraformrc to point to our freshly built provider!
	cwd, err := os.Getwd()
	require.NoError(t, err)
	tfrcPath := filepath.Join(cwd, "integration.tfrc")
	provPath, err := filepath.Abs("..")
	require.NoError(t, err)
	testTerraformrc := fmt.Sprintf(`provider_installation {
dev_overrides {
	"coder/coderd" = %q
	}
	direct{}
}`, provPath)
	err = os.WriteFile(tfrcPath, []byte(testTerraformrc), 0o644)
	require.NoError(t, err, "write terraformrc to tempdir")
	t.Cleanup(func() {
		// _ = os.Remove(tfrcPath)
	})
	return tfrcPath
}
