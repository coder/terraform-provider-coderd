package integration

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIntegration performs an integration test against an ephemeral Coder deployment.
// For each directory containing a `main.tf` under `/integration`, performs the following:
// - Creates a temporary Coder instance running in Docker
// - Runs the `main.tf` specified in the given test directory against the Coder deployment
// - Asserts the state of the deployment via `codersdk`.
func TestIntegration(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMins)*time.Minute)
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
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := StartCoder(ctx, t, tt.name, true)
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
