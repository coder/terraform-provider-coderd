package provider

import (
	"context"
	"fmt"
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
	client := integration.StartCoder(ctx, t, "user_acc")

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
	cfg2.Name = PtrTo("Example User New")

	resource.Test(t, resource.TestCase{
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
					resource.TestCheckResourceAttr("coderd_user.test", "roles.0", "auditor"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.1", "owner"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("coderd_user.test", "password", "SomeSecurePassword!"),
					resource.TestCheckResourceAttr("coderd_user.test", "suspended", "false"),
				),
			},
			// ImportState testing
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				// We can't pull the password from the API.
				ImportStateVerifyIgnore: []string{"password"},
			},
			// Update and Read testing
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User New"),
				),
			},
			// Delete testing automatically occurs in TestCase
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
		"orNull": func(v interface{}) string {
			if v == nil {
				return "null"
			}
			switch value := v.(type) {
			case *string:
				if value == nil {
					return "null"
				}
				return fmt.Sprintf("%q", *value)
			case *bool:
				if value == nil {
					return "null"
				}
				return fmt.Sprintf(`%t`, *value)
			case *[]string:
				if value == nil {
					return "null"
				}
				var result string
				for i, role := range *value {
					if i > 0 {
						result += ", "
					}
					result += fmt.Sprintf("%q", role)
				}
				return fmt.Sprintf("[%s]", result)

			default:
				require.NoError(t, fmt.Errorf("unknown type in template: %T", value))
				return ""
			}
		},
	}

	buf := strings.Builder{}
	tmpl, err := template.New("test").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)

	return buf.String()
}
