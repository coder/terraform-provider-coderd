package provider

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccOrganizationDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "org_data_acc", false)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	defaultCheckFn := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.coderd_organization.test", "id", firstUser.OrganizationIDs[0].String()),
		resource.TestCheckResourceAttr("data.coderd_organization.test", "is_default", "true"),
		resource.TestCheckResourceAttr("data.coderd_organization.test", "name", "first-organization"),
		resource.TestCheckResourceAttr("data.coderd_organization.test", "members.#", "1"),
		resource.TestCheckTypeSetElemAttr("data.coderd_organization.test", "members.*", firstUser.ID.String()),
		resource.TestCheckResourceAttrSet("data.coderd_organization.test", "created_at"),
		resource.TestCheckResourceAttrSet("data.coderd_organization.test", "updated_at"),
	)

	t.Run("DefaultOrgByIDOk", func(t *testing.T) {
		cfg := testAccOrganizationDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    PtrTo(firstUser.OrganizationIDs[0].String()),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  defaultCheckFn,
				},
			},
		})
	})

	t.Run("DefaultOrgByNameOk", func(t *testing.T) {
		cfg := testAccOrganizationDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  PtrTo("first-organization"),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  defaultCheckFn,
				},
			},
		})
	})

	t.Run("DefaultOrgByIsDefaultOk", func(t *testing.T) {
		cfg := testAccOrganizationDataSourceConfig{
			URL:       client.URL.String(),
			Token:     client.SessionToken(),
			IsDefault: PtrTo(true),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  defaultCheckFn,
				},
			},
		})
	})

	t.Run("InvalidAttributesError", func(t *testing.T) {
		cfg := testAccOrganizationDataSourceConfig{
			URL:       client.URL.String(),
			Token:     client.SessionToken(),
			IsDefault: PtrTo(true),
			Name:      PtrTo("first-organization"),
		}
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`Exactly one of these attributes must be configured: \[id,is\_default,name\]`),
				},
			},
		})
	})

	// TODO: Non-default org tests
}

type testAccOrganizationDataSourceConfig struct {
	URL   string
	Token string

	ID        *string
	Name      *string
	IsDefault *bool
}

func (c testAccOrganizationDataSourceConfig) String(t *testing.T) string {
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_organization" "test" {
	id         = {{orNull .ID}}
	name       = {{orNull .Name}}
	is_default = {{orNull .IsDefault}}
}
`

	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("groupDataSource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
