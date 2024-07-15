package provider

import (
	"context"
	"html/template"
	"strings"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccUserDataSource(t *testing.T) {
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "user_acc")
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
	cfg := testAccUserDataSourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
	}
	// User by Username
	cfg.Username = user.Username
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.coderd_user.test", "username", "example"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "name", "Example User"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "email", "example@coder.com"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "roles.#", "1"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "roles.0", "auditor"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "suspended", "false"),
				),
			},
		},
	})
	cfg.Username = ""
	cfg.ID = user.ID.String()
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// User by ID
		Steps: []resource.TestStep{
			{
				Config: cfg.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.coderd_user.test", "username", "example"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "name", "Example User"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "email", "example@coder.com"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "roles.#", "1"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "roles.0", "auditor"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "suspended", "false"),
				),
			},
		},
	})
}

type testAccUserDataSourceConfig struct {
	URL   string
	Token string

	ID       string
	Username string
}

func (c testAccUserDataSourceConfig) String(t *testing.T) string {
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_user" "test" {
{{- if .ID }}
  id = "{{ .ID }}"
{{- end }}
{{- if .Username }}
  username = "{{ .Username }}"
{{- end }}
}`

	tmpl := template.Must(template.New("userDataSource").Parse(tpl))

	buf := strings.Builder{}
	err := tmpl.Execute(&buf, c)
	if err != nil {
		panic(err)
	}

	return buf.String()
}
