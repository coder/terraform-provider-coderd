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
	"github.com/stretchr/testify/require"
)

func TestAccOrganizationResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "group_acc", true)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	user1, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
		Email:          "example@coder.com",
		Username:       "example",
		Password:       "SomeSecurePassword!",
		UserLoginType:  "password",
		OrganizationID: firstUser.OrganizationIDs[0],
	})
	require.NoError(t, err)

	user2, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
		Email:          "example2@coder.com",
		Username:       "example2",
		Password:       "SomeSecurePassword!",
		UserLoginType:  "password",
		OrganizationID: firstUser.OrganizationIDs[0],
	})
	require.NoError(t, err)

	cfg1 := testAccOrganizationResourceConfig{
		URL:         client.URL.String(),
		Token:       client.SessionToken(),
		Name:        ptr.Ref("example-org"),
		DisplayName: ptr.Ref("Example Organization"),
		Description: ptr.Ref("This is an example organization"),
		Icon:        ptr.Ref("/icon/coder.svg"),
		Members:     ptr.Ref([]string{user1.ID.String()}),
	}

	cfg2 := cfg1
	cfg2.Name = ptr.Ref("example-org-new")
	cfg2.DisplayName = ptr.Ref("Example Organization New")
	cfg2.Members = ptr.Ref([]string{user2.ID.String()})

	cfg3 := cfg2
	cfg3.Members = nil

	t.Run("CreateImportUpdateReadOk", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Create and Read
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_organization.test", "name", "example-org"),
						resource.TestCheckResourceAttr("coderd_organization.test", "display_name", "Example Organization"),
						resource.TestCheckResourceAttr("coderd_organization.test", "icon", "/icon/coder.svg"),
						resource.TestCheckResourceAttr("coderd_organization.test", "members.#", "1"),
						resource.TestCheckResourceAttr("coderd_organization.test", "members.0", user1.ID.String()),
					),
				},
				// Import
				{
					Config:                  cfg1.String(t),
					ResourceName:            "coderd_organization.test",
					ImportState:             true,
					ImportStateVerify:       true,
					ImportStateVerifyIgnore: []string{"members"},
				},
				// Update and Read
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_organization.test", "name", "example-org-new"),
						resource.TestCheckResourceAttr("coderd_organization.test", "display_name", "Example Organization New"),
						resource.TestCheckResourceAttr("coderd_organization.test", "members.#", "1"),
						resource.TestCheckResourceAttr("coderd_organization.test", "members.0", user2.ID.String()),
					),
				},
				// Unmanaged members
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_organization.test", "members"),
					),
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
	Members     *[]string
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
	members      = {{orNull .Members}}
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
