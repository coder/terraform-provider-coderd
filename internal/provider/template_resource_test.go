package provider

import (
	"context"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccTemplateResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "template_acc", true)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	cfg1 := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  PtrTo("example-template"),
		Versions: []testAccTemplateVersionConfig{
			{
				Name:      PtrTo("main"),
				Directory: PtrTo("../../integration/template-test/example-template/"),
				Active:    PtrTo(true),
				// TODO(ethanndickson): Remove this when we add in `*.tfvars` parsing
				TerraformVariables: []testAccTemplateKeyValueConfig{
					{
						Key:   PtrTo("name"),
						Value: PtrTo("world"),
					},
				},
			},
		},
		ACL: testAccTemplateACLConfig{
			GroupACL: []testAccTemplateKeyValueConfig{
				{
					Key:   PtrTo(firstUser.OrganizationIDs[0].String()),
					Value: PtrTo("use"),
				},
			},
		},
	}

	cfg2 := cfg1
	cfg2.Versions = slices.Clone(cfg2.Versions)
	cfg2.Name = PtrTo("example-template-new")
	cfg2.Versions[0].Directory = PtrTo("../../integration/template-test/example-template-2/")
	cfg2.Versions[0].Name = PtrTo("new")
	cfg2.ACL.UserACL = []testAccTemplateKeyValueConfig{
		{
			Key:   PtrTo(firstUser.ID.String()),
			Value: PtrTo("admin"),
		},
	}

	cfg3 := cfg2
	cfg3.Versions = slices.Clone(cfg3.Versions)
	cfg3.Versions = append(cfg3.Versions, testAccTemplateVersionConfig{
		Name:      PtrTo("legacy-template"),
		Directory: PtrTo("../../integration/template-test/example-template/"),
		Active:    PtrTo(false),
		TerraformVariables: []testAccTemplateKeyValueConfig{
			{
				Key:   PtrTo("name"),
				Value: PtrTo("world"),
			},
		},
	})

	cfg4 := cfg3
	cfg4.Versions = slices.Clone(cfg4.Versions)
	cfg4.Versions[0].Active = PtrTo(false)
	cfg4.Versions[1].Active = PtrTo(true)

	cfg5 := cfg4
	cfg5.Versions = slices.Clone(cfg5.Versions)
	cfg5.Versions[0], cfg5.Versions[1] = cfg5.Versions[1], cfg5.Versions[0]

	cfg6 := cfg4
	cfg6.Versions = slices.Clone(cfg6.Versions[1:])

	cfg7 := cfg6
	cfg7.ACL.null = true

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
					resource.TestCheckResourceAttr("coderd_template.test", "display_name", "example-template"),
					resource.TestCheckResourceAttr("coderd_template.test", "description", ""),
					resource.TestCheckResourceAttr("coderd_template.test", "organization_id", firstUser.OrganizationIDs[0].String()),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name":           regexp.MustCompile("main"),
						"id":             regexp.MustCompile(".*"),
						"directory_hash": regexp.MustCompile(".+"),
						"message":        regexp.MustCompile(""),
					}),
				),
			},
			// Import
			{
				Config:            cfg1.String(t),
				ResourceName:      "coderd_template.test",
				ImportState:       true,
				ImportStateVerify: true,
				// In the real world, `versions` needs to be added to the configuration after importing
				ImportStateVerifyIgnore: []string{"versions", "acl"},
			},
			// Update existing version & metadata
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
					resource.TestCheckResourceAttr("coderd_template.test", "name", "example-template-new"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name": regexp.MustCompile("new"),
					}),
				),
			},
			// Append version
			{
				Config: cfg3.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "2"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name": regexp.MustCompile("legacy-template"),
					}),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name": regexp.MustCompile("new"),
					}),
				),
			},
			// Change active version
			{
				Config: cfg4.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "2"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"active": regexp.MustCompile("true"),
						"name":   regexp.MustCompile("legacy-template"),
					}),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"active": regexp.MustCompile("false"),
						"name":   regexp.MustCompile("new"),
					}),
				),
			},
			// Swap versions
			{
				Config: cfg5.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "2"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"active": regexp.MustCompile("true"),
						"name":   regexp.MustCompile("legacy-template"),
					}),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"active": regexp.MustCompile("false"),
						"name":   regexp.MustCompile("new"),
					}),
				),
			},
			// Delete version at index 0
			{
				Config: cfg6.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "1"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"active": regexp.MustCompile("true"),
						"name":   regexp.MustCompile("legacy-template"),
					}),
				),
			},
			// Unmanaged ACL
			{
				Config: cfg7.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("coderd_template.test", "acl"),
				),
			},
		},
	})
}

type testAccTemplateResourceConfig struct {
	URL   string
	Token string

	Name           *string
	DisplayName    *string
	Description    *string
	OrganizationID *string
	Versions       []testAccTemplateVersionConfig
	ACL            testAccTemplateACLConfig
}

type testAccTemplateACLConfig struct {
	null     bool
	GroupACL []testAccTemplateKeyValueConfig
	UserACL  []testAccTemplateKeyValueConfig
}

func (c testAccTemplateACLConfig) String(t *testing.T) string {
	if c.null == true {
		return "null"
	}
	t.Helper()
	tpl := `{
		groups = [
			{{- range .GroupACL}}
			{
				id   = {{orNull .Key}}
				role = {{orNull .Value}}
			},
			{{- end}}
		]
		users = [
			{{- range .UserACL}}
			{
				id   = {{orNull .Key}}
				role = {{orNull .Value}}
			},
			{{- end}}
		]
	}
	`

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

func (c testAccTemplateResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url   = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_template" "test" {
	name            = {{orNull .Name}}
	display_name    = {{orNull .DisplayName}}
	description     = {{orNull .Description}}
	organization_id = {{orNull .OrganizationID}}

	acl = ` + c.ACL.String(t) + `

	versions = [
	{{- range .Versions }}
	{
		name      = {{orNull .Name}}
		directory = {{orNull .Directory}}
		active    = {{orNull .Active}}

		tf_vars = [
			{{- range .TerraformVariables }}
			{
				name  = {{orNull .Key}}
				value = {{orNull .Value}}
			},
			{{- end}}
		]
	},
	{{- end}}
	]
}
`

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

type testAccTemplateVersionConfig struct {
	Name               *string
	Message            *string
	Directory          *string
	Active             *bool
	TerraformVariables []testAccTemplateKeyValueConfig
}

type testAccTemplateKeyValueConfig struct {
	Key   *string
	Value *string
}
