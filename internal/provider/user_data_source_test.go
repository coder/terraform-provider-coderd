package provider

/*
import (
	"html/template"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccUserDataSource(t *testing.T) {
	// User by Username
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserDataSourceConfig{
					Username: "example",
				}.String(t),
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
		},
	})
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// User by ID
		Steps: []resource.TestStep{
			{
				Config: testAccUserDataSourceConfig{
					ID: "example",
				}.String(t),
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
		},
	})
}

type testAccUserDataSourceConfig struct {
	ID       string
	Username string
}

func (c testAccUserDataSourceConfig) String(t *testing.T) string {
	tpl := `
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
*/
