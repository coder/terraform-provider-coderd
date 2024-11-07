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
	"github.com/coder/terraform-provider-coderd/internal"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccGroupDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "group_data_acc", true)
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

	group, err := client.CreateGroup(ctx, firstUser.OrganizationIDs[0], codersdk.CreateGroupRequest{
		Name:           "example-group",
		DisplayName:    "Example Group",
		AvatarURL:      "https://google.com",
		QuotaAllowance: 10,
	})
	require.NoError(t, err)
	group, err = client.PatchGroup(ctx, group.ID, codersdk.PatchGroupRequest{
		AddUsers: []string{user1.ID.String(), user2.ID.String()},
	})
	require.NoError(t, err)

	checkFn := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.coderd_group.test", "id", group.ID.String()),
		resource.TestCheckResourceAttr("data.coderd_group.test", "name", "example-group"),
		resource.TestCheckResourceAttr("data.coderd_group.test", "organization_id", firstUser.OrganizationIDs[0].String()),
		resource.TestCheckResourceAttr("data.coderd_group.test", "display_name", "Example Group"),
		resource.TestCheckResourceAttr("data.coderd_group.test", "avatar_url", "https://google.com"),
		resource.TestCheckResourceAttr("data.coderd_group.test", "quota_allowance", "10"),
		resource.TestCheckResourceAttr("data.coderd_group.test", "members.#", "2"),
		resource.TestCheckTypeSetElemNestedAttrs("data.coderd_group.test", "members.*", map[string]string{
			"id": user1.ID.String(),
		}),
		resource.TestCheckTypeSetElemNestedAttrs("data.coderd_group.test", "members.*", map[string]string{
			"id": user2.ID.String(),
		}),
		resource.TestCheckResourceAttr("data.coderd_group.test", "source", "user"),
	)

	t.Run("GroupByIDOk", func(t *testing.T) {
		cfg := testAccGroupDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    ptr.Ref(group.ID.String()),
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

	t.Run("GroupByNameAndOrganizationIDOk", func(t *testing.T) {
		cfg := testAccGroupDataSourceConfig{
			URL:            client.URL.String(),
			Token:          client.SessionToken(),
			OrganizationID: ptr.Ref(firstUser.OrganizationIDs[0].String()),
			Name:           ptr.Ref("example-group"),
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

	t.Run("UseDefaultOrganizationIDOk", func(t *testing.T) {
		cfg := testAccGroupDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-group"),
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

	t.Run("OrgIDOnlyError", func(t *testing.T) {
		cfg := testAccGroupDataSourceConfig{
			URL:            client.URL.String(),
			Token:          client.SessionToken(),
			OrganizationID: ptr.Ref(firstUser.OrganizationIDs[0].String()),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// Neither ID nor Username
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`At least one attribute out of \[name,id\] must be specified`),
				},
			},
		})
	})

	t.Run("NoneError", func(t *testing.T) {
		cfg := testAccGroupDataSourceConfig{
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
					ExpectError: regexp.MustCompile(`At least one attribute out of \[name,id\] must be specified`),
				},
			},
		})
	})
}

type testAccGroupDataSourceConfig struct {
	URL   string
	Token string

	ID             *string
	Name           *string
	OrganizationID *string
}

func (c testAccGroupDataSourceConfig) String(t *testing.T) string {
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_group" "test" {
	id              = {{orNull .ID}}
	name            = {{orNull .Name}}
	organization_id = {{orNull .OrganizationID}}
}
`

	funcMap := template.FuncMap{
		"orNull": internal.PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("groupDataSource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
