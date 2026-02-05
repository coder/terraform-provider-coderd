package provider

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccWorkspaceProxyResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ws_proxy_acc", integration.UseLicense)

	cfg1 := testAccWorkspaceProxyResourceConfig{
		URL:         client.URL.String(),
		Token:       client.SessionToken(),
		Name:        ptr.Ref("example"),
		DisplayName: ptr.Ref("Example WS Proxy"),
		Icon:        ptr.Ref("/emojis/1f407.png"),
	}

	cfg2 := cfg1
	cfg2.Name = ptr.Ref("example-new")
	cfg2.DisplayName = ptr.Ref("Example WS Proxy New")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_workspace_proxy.test", "session_token"),
				),
			},
			// Update and Read testing
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_workspace_proxy.test", "session_token")),
			},
		},
	})
}

func TestAccWorkspaceProxyResourceAGPL(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "ws_proxy_acc_agpl")

	cfg1 := testAccWorkspaceProxyResourceConfig{
		URL:         client.URL.String(),
		Token:       client.SessionToken(),
		Name:        ptr.Ref("example"),
		DisplayName: ptr.Ref("Example WS Proxy"),
		Icon:        ptr.Ref("/emojis/1f407.png"),
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg1.String(t),
				ExpectError: regexp.MustCompile("Your license is not entitled to create workspace proxies."),
			},
		},
	})

}

type testAccWorkspaceProxyResourceConfig struct {
	URL   string
	Token string

	Name        *string
	DisplayName *string
	Icon        *string
}

func (c testAccWorkspaceProxyResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_workspace_proxy" "test" {
	name = {{orNull .Name}}
	display_name = {{orNull .DisplayName}}
	icon = {{orNull .Icon}}
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
