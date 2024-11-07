package provider

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccUserDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "user_data_acc", false)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	user, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
		Email:          "example@coder.com",
		Username:       "example",
		Password:       "SomeSecurePassword!",
		UserLoginType:  "password",
		OrganizationID: firstUser.OrganizationIDs[0],
	})
	require.NoError(t, err)
	_, err = client.UpdateUserRoles(ctx, user.Username, codersdk.UpdateRoles{
		Roles: []string{"auditor"},
	})
	require.NoError(t, err)
	_, err = client.UpdateUserProfile(ctx, user.Username, codersdk.UpdateUserProfileRequest{
		Username: user.Username,
		Name:     "Example User",
	})
	require.NoError(t, err)

	checkFn := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.coderd_user.test", "username", "example"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "name", "Example User"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "email", "example@coder.com"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "roles.#", "1"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "roles.0", "auditor"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "login_type", "password"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "suspended", "false"),
	)
	t.Run("UserByUsernameOk", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:      client.URL.String(),
			Token:    client.SessionToken(),
			Username: ptr.Ref(user.Username),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  checkFn,
				},
			},
		})
	})

	t.Run("UserByIDOk", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    ptr.Ref(user.ID.String()),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// User by ID
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  checkFn,
				},
			},
		})
	})
	t.Run("NeitherIDNorUsernameError", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// Neither ID nor Username
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`At least one of these attributes must be configured: \[id,username\]`),
				},
			},
		})
	})

}

type testAccUserDataSourceConfig struct {
	URL   string
	Token string

	ID       *string
	Username *string
}

func (c testAccUserDataSourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_user" "test" {
	id       = {{orNull .ID}}
	username = {{orNull .Username}}
}`

	funcMap := template.FuncMap{
		"orNull": printOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("userDataSource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
