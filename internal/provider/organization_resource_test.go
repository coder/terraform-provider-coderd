package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestAccOrganizationResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "group_acc", true)
	_, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	cfg1 := testAccOrganizationResourceConfig{
		URL:         client.URL.String(),
		Token:       client.SessionToken(),
		Name:        ptr.Ref("example-org"),
		DisplayName: ptr.Ref("Example Organization"),
		Description: ptr.Ref("This is an example organization"),
		Icon:        ptr.Ref("/icon/coder.svg"),
	}

	cfg2 := cfg1
	cfg2.Name = ptr.Ref("example-org-new")
	cfg2.DisplayName = ptr.Ref("Example Organization New")

	cfg3 := cfg2

	t.Run("CreateImportUpdateReadOk", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Create and Read
				{
					Config: cfg1.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization.test", tfjsonpath.New("name"), knownvalue.StringExact("example-org")),
						statecheck.ExpectKnownValue("coderd_organization.test", tfjsonpath.New("display_name"), knownvalue.StringExact("Example Organization")),
						statecheck.ExpectKnownValue("coderd_organization.test", tfjsonpath.New("icon"), knownvalue.StringExact("/icon/coder.svg")),
					},
				},
				// Import
				{
					Config:            cfg1.String(t),
					ResourceName:      "coderd_organization.test",
					ImportState:       true,
					ImportStateVerify: true,
				},
				// Update and Read
				{
					Config: cfg2.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization.test", tfjsonpath.New("name"), knownvalue.StringExact("example-org-new")),
						statecheck.ExpectKnownValue("coderd_organization.test", tfjsonpath.New("display_name"), knownvalue.StringExact("Example Organization New")),
					},
				},
			},
		})
	})

	t.Run("CreateUnmanagedMembersOk", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_organization.test", "members"),
					),
				},
			},
		})
	})
}

type testAccOrganizationResourceConfig struct {
	URL   string
	Token string

	Name        *string
	DisplayName *string
	Description *string
	Icon        *string
}

func (c testAccOrganizationResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_organization" "test" {
	name         = {{orNull .Name}}
	display_name = {{orNull .DisplayName}}
	description  = {{orNull .Description}}
	icon         = {{orNull .Icon}}
}
`
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("organizationResource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
