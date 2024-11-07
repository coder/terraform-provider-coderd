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

func TestAccGroupResource(t *testing.T) {
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

	cfg1 := testAccGroupResourceconfig{
		URL:            client.URL.String(),
		Token:          client.SessionToken(),
		Name:           ptr.Ref("example-group"),
		DisplayName:    ptr.Ref("Example Group"),
		AvatarUrl:      ptr.Ref("https://google.com"),
		QuotaAllowance: ptr.Ref(int32(100)),
		Members:        ptr.Ref([]string{user1.ID.String()}),
	}

	cfg2 := cfg1
	cfg2.Name = ptr.Ref("example-group-new")
	cfg2.DisplayName = ptr.Ref("Example Group New")
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
						resource.TestCheckResourceAttr("coderd_group.test", "name", "example-group"),
						resource.TestCheckResourceAttr("coderd_group.test", "display_name", "Example Group"),
						resource.TestCheckResourceAttr("coderd_group.test", "avatar_url", "https://google.com"),
						resource.TestCheckResourceAttr("coderd_group.test", "quota_allowance", "100"),
						resource.TestCheckResourceAttr("coderd_group.test", "organization_id", firstUser.OrganizationIDs[0].String()),
						resource.TestCheckResourceAttr("coderd_group.test", "members.#", "1"),
						resource.TestCheckResourceAttr("coderd_group.test", "members.0", user1.ID.String()),
					),
				},
				// Import by ID
				{
					ResourceName:            "coderd_group.test",
					ImportState:             true,
					ImportStateVerify:       true,
					ImportStateVerifyIgnore: []string{"members"},
				},
				// Import by org name and group name
				{
					ResourceName:            "coderd_group.test",
					ImportState:             true,
					ImportStateId:           "default/example-group",
					ImportStateVerify:       true,
					ImportStateVerifyIgnore: []string{"members"},
				},
				// Update and Read
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_group.test", "name", "example-group-new"),
						resource.TestCheckResourceAttr("coderd_group.test", "display_name", "Example Group New"),
						resource.TestCheckResourceAttr("coderd_group.test", "members.#", "1"),
						resource.TestCheckResourceAttr("coderd_group.test", "members.0", user2.ID.String()),
					),
				},
				// Unmanaged members
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_group.test", "members"),
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
						resource.TestCheckNoResourceAttr("coderd_group.test", "members"),
					),
				},
			},
		})
	})
}

func TestAccGroupResourceAGPL(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "group_acc_agpl", false)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	cfg1 := testAccGroupResourceconfig{
		URL:            client.URL.String(),
		Token:          client.SessionToken(),
		Name:           ptr.Ref("example-group"),
		DisplayName:    ptr.Ref("Example Group"),
		AvatarUrl:      ptr.Ref("https://google.com"),
		QuotaAllowance: ptr.Ref(int32(100)),
		Members:        ptr.Ref([]string{firstUser.ID.String()}),
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg1.String(t),
				ExpectError: regexp.MustCompile("Your license is not entitled to use groups."),
			},
		},
	})

}

type testAccGroupResourceconfig struct {
	URL   string
	Token string

	Name           *string
	DisplayName    *string
	AvatarUrl      *string
	QuotaAllowance *int32
	OrganizationID *string
	Members        *[]string
}

func (c testAccGroupResourceconfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url   = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_group" "test" {
	name              = {{orNull .Name}}
	display_name      = {{orNull .DisplayName}}
	avatar_url        = {{orNull .AvatarUrl}}
	quota_allowance   = {{orNull .QuotaAllowance}}
	organization_id   = {{orNull .OrganizationID}}
	members           = {{orNull .Members}}
}
`
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("groupResource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
