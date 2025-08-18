package provider

import (
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/stretchr/testify/require"
)

func TestAccUserResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "user_acc", false)

	cfg1 := testAccUserResourceConfig{
		URL:       client.URL.String(),
		Token:     client.SessionToken(),
		Username:  ptr.Ref("example"),
		Name:      ptr.Ref("Example User"),
		Email:     ptr.Ref("example@coder.com"),
		Roles:     ptr.Ref([]string{"owner", "auditor"}),
		LoginType: ptr.Ref("password"),
		Password:  ptr.Ref("SomeSecurePassword!"),
	}

	cfg2 := cfg1
	cfg2.Username = ptr.Ref("exampleNew")

	cfg3 := cfg2
	cfg3.Name = ptr.Ref("Example New")

	cfg4 := cfg3
	cfg4.LoginType = ptr.Ref("github")
	cfg4.Password = nil

	cfg5 := cfg4
	cfg5.Roles = nil

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "example"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", "example@coder.com"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.#", "2"),
					resource.TestCheckTypeSetElemAttr("coderd_user.test", "roles.*", "auditor"),
					resource.TestCheckTypeSetElemAttr("coderd_user.test", "roles.*", "owner"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("coderd_user.test", "password", "SomeSecurePassword!"),
					resource.TestCheckResourceAttr("coderd_user.test", "suspended", "false"),
				),
			},
			// Import by ID
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				// We can't pull the password from the API.
				ImportStateVerifyIgnore: []string{"password"},
			},
			// ImportState by username
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "example",
				// We can't pull the password from the API.
				ImportStateVerifyIgnore: []string{"password"},
			},
			// Update and Read testing
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User"),
				),
			},
			{
				Config: cfg3.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example New"),
				),
			},
			// Replace triggered
			{
				Config: cfg4.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "github"),
				),
			},
			// Verify config drift via deletion is handled
			{
				Config: cfg4.String(t),
				Check: func(*terraform.State) error {
					user, err := client.User(ctx, "exampleNew")
					if err != nil {
						return err
					}
					return client.DeleteUser(ctx, user.ID)
				},
				// The Plan should be to create the entire resource
				ExpectNonEmptyPlan: true,
			},
			// Unmanaged roles
			{
				Config: cfg5.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("coderd_user.test", "roles"),
				),
			},
		},
	})

	t.Run("CreateUnmanagedRolesOk", func(t *testing.T) {
		cfg := testAccUserResourceConfig{
			URL:       client.URL.String(),
			Token:     client.SessionToken(),
			Username:  ptr.Ref("unmanaged"),
			Name:      ptr.Ref("Unmanaged User"),
			Email:     ptr.Ref("unmanaged@coder.com"),
			Roles:     nil, // Start with unmanaged roles
			LoginType: ptr.Ref("password"),
			Password:  ptr.Ref("SomeSecurePassword!"),
		}

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_user.test", "roles"),
					),
				},
			},
		})
	})
}

type testAccUserResourceConfig struct {
	URL   string
	Token string

	Username  *string
	Name      *string
	Email     *string
	Roles     *[]string
	LoginType *string
	Password  *string
	Suspended *bool
}

func (c testAccUserResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_user" "test" {
	username   = {{orNull .Username}}
	name       = {{orNull .Name}}
	email      = {{orNull .Email}}
	roles      = {{orNull .Roles}}
	login_type = {{orNull .LoginType}}
	password   = {{orNull .Password}}
	suspended  = {{orNull .Suspended}}
}
`
	// Define template functions
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("test").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)

	return buf.String()
}
