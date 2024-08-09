package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"text/template"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	cp "github.com/otiai10/copy"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
)

func TestAccTemplateResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "template_acc", true)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	exTemplateOne := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	exTemplateTwo := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template-2", exTemplateTwo)
	require.NoError(t, err)

	t.Run("BasicUsage", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  PtrTo("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
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
		cfg2.AllowUserAutostart = PtrTo(false)
		cfg2.Versions[0].Directory = &exTemplateTwo
		cfg2.Versions[0].Name = PtrTo("new")
		cfg2.ACL.UserACL = []testAccTemplateKeyValueConfig{
			{
				Key:   PtrTo(firstUser.ID.String()),
				Value: PtrTo("admin"),
			},
		}
		cfg2.AutostopRequirement = testAccAutostopRequirementConfig{
			DaysOfWeek: PtrTo([]string{"monday", "tuesday"}),
			Weeks:      PtrTo(int64(2)),
		}

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions = append(cfg3.Versions, testAccTemplateVersionConfig{
			Name:      PtrTo("legacy-template"),
			Directory: &exTemplateOne,
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
				// Init, creates the first version
				{
					Config: cfg1.String(t),
					Check: resource.ComposeTestCheckFunc(
						resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
						resource.TestCheckResourceAttr("coderd_template.test", "display_name", "example-template"),
						resource.TestCheckResourceAttr("coderd_template.test", "description", ""),
						resource.TestCheckResourceAttr("coderd_template.test", "organization_id", firstUser.OrganizationIDs[0].String()),
						resource.TestCheckResourceAttr("coderd_template.test", "icon", ""),
						resource.TestCheckResourceAttr("coderd_template.test", "default_ttl_ms", "0"),
						resource.TestCheckResourceAttr("coderd_template.test", "activity_bump_ms", "3600000"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.days_of_week.#", "0"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.weeks", "1"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_start_permitted_days_of_week.#", "7"),
						resource.TestCheckResourceAttr("coderd_template.test", "allow_user_cancel_workspace_jobs", "true"),
						resource.TestCheckResourceAttr("coderd_template.test", "allow_user_auto_start", "true"),
						resource.TestCheckResourceAttr("coderd_template.test", "allow_user_auto_stop", "true"),
						resource.TestCheckResourceAttr("coderd_template.test", "failure_ttl_ms", "0"),
						resource.TestCheckResourceAttr("coderd_template.test", "time_til_dormant_ms", "0"),
						resource.TestCheckResourceAttr("coderd_template.test", "time_til_dormant_autodelete_ms", "0"),
						resource.TestCheckResourceAttr("coderd_template.test", "require_active_version", "false"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"name":           regexp.MustCompile(".+"),
							"id":             regexp.MustCompile(".+"),
							"directory_hash": regexp.MustCompile(".+"),
							"message":        regexp.MustCompile(""),
						}),
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				// Modify template contents. Creates a second version.
				{
					Config: cfg1.String(t),
					PreConfig: func() {
						file := fmt.Sprintf("%s/terraform.tfvars", *cfg1.Versions[0].Directory)
						newFile := []byte("name = \"world2\"")
						err := os.WriteFile(file, newFile, 0644)
						require.NoError(t, err)
					},
					Check: testAccCheckNumTemplateVersions(ctx, client, 2),
					// Version should be updated, checked at the end
				},
				// Undo modification. Creates a third version since it differs from the last apply
				{
					Config: cfg1.String(t),
					PreConfig: func() {
						file := fmt.Sprintf("%s/terraform.tfvars", *cfg1.Versions[0].Directory)
						newFile := []byte("name = \"world\"")
						err := os.WriteFile(file, newFile, 0644)
						require.NoError(t, err)
					},
					Check: testAccCheckNumTemplateVersions(ctx, client, 3),
				},
				// Import
				{
					Config:            cfg1.String(t),
					ResourceName:      "coderd_template.test",
					ImportState:       true,
					ImportStateVerify: true,
					// In the real world, `versions` needs to be added to the configuration after importing
					// We can't import ACL as we can't currently differentiate between managed and unmanaged ACL
					ImportStateVerifyIgnore: []string{"versions", "acl"},
				},
				// Change existing version directory & name, update template metadata. Creates a fourth version.
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
						resource.TestCheckResourceAttr("coderd_template.test", "name", "example-template-new"),
						resource.TestCheckResourceAttr("coderd_template.test", "allow_user_auto_start", "false"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.days_of_week.#", "2"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.weeks", "2"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"name": regexp.MustCompile("new"),
						}),
						testAccCheckNumTemplateVersions(ctx, client, 4),
					),
				},
				// Append version. Creates a fifth version
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
						testAccCheckNumTemplateVersions(ctx, client, 5),
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
				// Swap versions in-place
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
						testAccCheckNumTemplateVersions(ctx, client, 5),
					),
				},
				// Resource deleted
			},
		})
	})

	t.Run("IdenticalVersions", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  PtrTo("example-template2"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: PtrTo("../../integration/template-test/example-template-2/"),
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   PtrTo("name"),
							Value: PtrTo("world"),
						},
					},
					Active: PtrTo(true),
				},
				{
					// Auto-generated version name
					Directory: PtrTo("../../integration/template-test/example-template-2/"),
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   PtrTo("name"),
							Value: PtrTo("world"),
						},
					},
				},
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[1].Name = PtrTo("new-name")

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions[0].Name = PtrTo("new-name-one")
		cfg3.Versions[1].Name = PtrTo("new-name-two")
		cfg3.Versions[0], cfg3.Versions[1] = cfg3.Versions[1], cfg3.Versions[0]

		cfg4 := cfg1
		cfg4.Versions = slices.Clone(cfg4.Versions)
		cfg4.Versions[0].Directory = PtrTo("../../integration/template-test/example-template/")

		cfg5 := cfg4
		cfg5.Versions = slices.Clone(cfg5.Versions)
		cfg5.Versions[1].Directory = PtrTo("../../integration/template-test/example-template/")

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Create two identical versions
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
					),
				},
				// Change the name of the second version
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "2"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("true"),
							"name":   regexp.MustCompile(".+"),
						}),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("false"),
							"name":   regexp.MustCompile("^new-name$"),
						}),
					),
				},
				// Swap the two versions, give them both new names
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "2"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("true"),
							"name":   regexp.MustCompile("^new-name-one$"),
						}),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("false"),
							"name":   regexp.MustCompile("^new-name-two$"),
						}),
						testAccCheckNumTemplateVersions(ctx, client, 2),
					),
				},
				// Change the first version's contents
				{
					Config: cfg4.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 3),
					),
				},
				// Change the second version's contents to match the first
				{
					Config: cfg5.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 4),
					),
				},
			},
		})
	})
}

func TestAccTemplateResourceAGPL(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "template_acc", false)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	cfg1 := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  PtrTo("example-template"),
		Versions: []testAccTemplateVersionConfig{
			{
				// Auto-generated version name
				Directory: PtrTo("../../integration/template-test/example-template/"),
				Active:    PtrTo(true),
			},
		},
		AllowUserAutostart: PtrTo(false),
	}

	cfg2 := cfg1
	cfg2.AllowUserAutostart = nil
	cfg2.AutostopRequirement.DaysOfWeek = PtrTo([]string{"monday", "tuesday"})

	cfg3 := cfg2
	cfg3.AutostopRequirement.null = true
	cfg3.AutostartRequirement = PtrTo([]string{})

	cfg4 := cfg3
	cfg4.FailureTTL = PtrTo(int64(1))

	cfg5 := cfg4
	cfg5.FailureTTL = nil
	cfg5.AutostartRequirement = nil
	cfg5.RequireActiveVersion = PtrTo(true)

	cfg6 := cfg5
	cfg6.RequireActiveVersion = nil
	cfg6.ACL = testAccTemplateACLConfig{
		GroupACL: []testAccTemplateKeyValueConfig{
			{
				Key:   PtrTo(firstUser.OrganizationIDs[0].String()),
				Value: PtrTo("use"),
			},
		},
	}

	for _, cfg := range []testAccTemplateResourceConfig{cfg1, cfg2, cfg3, cfg4} {
		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile("Your license is not entitled to use advanced template scheduling"),
				},
			},
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg5.String(t),
				ExpectError: regexp.MustCompile("Your license is not entitled to use access control"),
			},
			{
				Config:      cfg6.String(t),
				ExpectError: regexp.MustCompile("Your license is not entitled to use template access control"),
			},
		},
	})
}

type testAccTemplateResourceConfig struct {
	URL   string
	Token string

	Name                         *string
	DisplayName                  *string
	Description                  *string
	OrganizationID               *string
	Icon                         *string
	DefaultTTL                   *int64
	ActivityBump                 *int64
	AutostopRequirement          testAccAutostopRequirementConfig
	AutostartRequirement         *[]string
	AllowUserCancelWorkspaceJobs *bool
	AllowUserAutostart           *bool
	AllowUserAutostop            *bool
	FailureTTL                   *int64
	TimeTilDormant               *int64
	TimeTilDormantAutodelete     *int64
	RequireActiveVersion         *bool
	DeprecationMessage           *string

	Versions []testAccTemplateVersionConfig
	ACL      testAccTemplateACLConfig
}

type testAccTemplateACLConfig struct {
	null     bool
	GroupACL []testAccTemplateKeyValueConfig
	UserACL  []testAccTemplateKeyValueConfig
}

func (c testAccTemplateACLConfig) String(t *testing.T) string {
	t.Helper()
	if c.null == true {
		return "null"
	}
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

type testAccAutostopRequirementConfig struct {
	null       bool
	DaysOfWeek *[]string
	Weeks      *int64
}

func (c testAccAutostopRequirementConfig) String(t *testing.T) string {
	t.Helper()
	if c.null == true {
		return "null"
	}
	tpl := `{
		days_of_week = {{orNull .DaysOfWeek}}
		weeks        = {{orNull .Weeks}}
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
	name                              = {{orNull .Name}}
	display_name                      = {{orNull .DisplayName}}
	description                       = {{orNull .Description}}
	organization_id                   = {{orNull .OrganizationID}}
	icon                              = {{orNull .Icon}}
	default_ttl_ms                    = {{orNull .DefaultTTL}}
	activity_bump_ms                  = {{orNull .ActivityBump}}
	auto_stop_requirement             = ` + c.AutostopRequirement.String(t) + `
	auto_start_permitted_days_of_week = {{orNull .AutostartRequirement}}
	allow_user_cancel_workspace_jobs  = {{orNull .AllowUserCancelWorkspaceJobs}}
	allow_user_auto_start             = {{orNull .AllowUserAutostart}}
	allow_user_auto_stop              = {{orNull .AllowUserAutostop}}
	failure_ttl_ms                    = {{orNull .FailureTTL}}
	time_til_dormant_ms               = {{orNull .TimeTilDormant}}
	time_til_dormant_autodelete_ms    = {{orNull .TimeTilDormantAutodelete}}
	require_active_version            = {{orNull .RequireActiveVersion}}
	deprecation_message               = {{orNull .DeprecationMessage}}

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

func testAccCheckNumTemplateVersions(ctx context.Context, client *codersdk.Client, expected int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		templates, err := client.Templates(ctx, codersdk.TemplateFilter{})
		if err != nil {
			return err
		}
		if len(templates) != 1 {
			return fmt.Errorf("expected 1 template, got %d", len(templates))
		}
		versions, err := client.TemplateVersionsByTemplate(ctx, codersdk.TemplateVersionsByTemplateRequest{
			TemplateID: templates[0].ID,
		})
		if err != nil {
			return err
		}
		if len(versions) != expected {
			return fmt.Errorf("expected %d versions, got %d", expected, len(versions))
		}
		return nil
	}
}

func TestReconcileVersionIDs(t *testing.T) {
	aUUID := uuid.New()
	bUUID := uuid.New()
	cases := []struct {
		Name             string
		planVersions     Versions
		configVersions   Versions
		inputState       LastVersionsByHash
		expectedVersions Versions
	}{
		{
			Name: "IdenticalDontRename",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
				{
					Name: types.StringValue("bar"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "bar",
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
				},
			},
		},
		{
			Name: "IdenticalRenameFirst",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
				{
					Name: types.StringValue("bar"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "baz",
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
			},
		},
		{
			Name: "IdenticalHashesInState",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
				{
					Name: types.StringValue("bar"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "qux",
					},
					{
						ID:   bUUID,
						Name: "baz",
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
				},
				{
					Name:          types.StringValue("bar"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(bUUID),
				},
			},
		},
		{
			Name: "UnknownUsesStateInOrder",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
				{
					Name:          types.StringUnknown(),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
				{
					Name: types.StringValue("bar"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "qux",
					},
					{
						ID:   bUUID,
						Name: "baz",
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
				},
				{
					Name:          types.StringValue("baz"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(bUUID),
				},
			},
		},
		{
			Name: "NewVersionNewRandomName",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("weird_draught12"),
					DirectoryHash: types.StringValue("bbb"),
					ID:            UUIDValue(aUUID),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringNull(),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "weird_draught12",
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringUnknown(),
					DirectoryHash: types.StringValue("bbb"),
					ID:            NewUUIDUnknown(),
				},
			},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			c.planVersions.reconcileVersionIDs(c.inputState, c.configVersions)
			require.Equal(t, c.expectedVersions, c.planVersions)
		})

	}
}
