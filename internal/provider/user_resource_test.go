package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccUserResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "user_acc", false)

	cfg1 := testAccUserResourceConfig{
		URL:       client.URL.String(),
		Token:     client.SessionToken(),
		Username:  PtrTo("example"),
		Name:      PtrTo("Example User"),
		Email:     PtrTo("example@coder.com"),
		Roles:     PtrTo([]string{"owner", "auditor"}),
		LoginType: PtrTo("password"),
		Password:  PtrTo("SomeSecurePassword!"),
	}

	cfg2 := cfg1
	cfg2.Username = PtrTo("exampleNew")

	cfg3 := cfg2
	cfg3.Name = PtrTo("Example New")

	cfg4 := cfg3
	cfg4.LoginType = PtrTo("github")
	cfg4.Password = nil

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
		},
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
