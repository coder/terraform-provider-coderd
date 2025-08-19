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

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
)

func TestAccTemplateResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_acc", false)
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
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Name = ptr.Ref("example-template-new")
		cfg2.Versions[0].Directory = &exTemplateTwo
		cfg2.Versions[0].Name = ptr.Ref("new")

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions = append(cfg3.Versions, testAccTemplateVersionConfig{
			Name:      ptr.Ref("legacy-template"),
			Directory: &exTemplateOne,
			Active:    ptr.Ref(false),
			TerraformVariables: []testAccTemplateKeyValueConfig{
				{
					Key:   ptr.Ref("name"),
					Value: ptr.Ref("world"),
				},
			},
		})

		cfg4 := cfg3
		cfg4.Versions = slices.Clone(cfg4.Versions)
		cfg4.Versions[0].Active = ptr.Ref(false)
		cfg4.Versions[1].Active = ptr.Ref(true)

		cfg5 := cfg4
		cfg5.Versions = slices.Clone(cfg5.Versions)
		cfg5.Versions[0], cfg5.Versions[1] = cfg5.Versions[1], cfg5.Versions[0]

		cfg6 := cfg4
		cfg6.Versions = slices.Clone(cfg6.Versions[1:])

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
						resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "public"),
						resource.TestCheckResourceAttr("coderd_template.test", "use_classic_parameter_flow", "false"),
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
				// Import by ID
				{
					Config:            cfg1.String(t),
					ResourceName:      "coderd_template.test",
					ImportState:       true,
					ImportStateVerify: true,
					// In the real world, `versions` needs to be added to the configuration after importing
					// We can't import ACL as we can't currently differentiate between managed and unmanaged ACL
					ImportStateVerifyIgnore: []string{"versions", "acl"},
				},
				// Import by org name and template name
				{
					ResourceName:            "coderd_template.test",
					ImportState:             true,
					ImportStateVerify:       true,
					ImportStateId:           "default/example-template",
					ImportStateVerifyIgnore: []string{"versions", "acl"},
				},
				// Change existing version directory & name, update template metadata. Creates a fourth version.
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
						resource.TestCheckResourceAttr("coderd_template.test", "name", "example-template-new"),
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
				// Resource deleted
			},
		})
	})

	t.Run("IdenticalVersions", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template2"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   ptr.Ref("name"),
							Value: ptr.Ref("world"),
						},
					},
					Active: ptr.Ref(true),
				},
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   ptr.Ref("name"),
							Value: ptr.Ref("world"),
						},
					},
					Active: ptr.Ref(false),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[1].Name = ptr.Ref("new-name")

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions[0].Name = ptr.Ref("new-name-one")
		cfg3.Versions[1].Name = ptr.Ref("new-name-two")
		cfg3.Versions[0], cfg3.Versions[1] = cfg3.Versions[1], cfg3.Versions[0]

		cfg4 := cfg1
		cfg4.Versions = slices.Clone(cfg4.Versions)
		cfg4.Versions[0].Directory = &exTemplateOne

		cfg5 := cfg4
		cfg5.Versions = slices.Clone(cfg5.Versions)
		cfg5.Versions[1].Directory = &exTemplateOne

		cfg6 := cfg5
		cfg6.Versions = slices.Clone(cfg6.Versions)
		cfg6.Versions[0].TerraformVariables = []testAccTemplateKeyValueConfig{
			{
				Key:   ptr.Ref("name"),
				Value: ptr.Ref("world2"),
			},
		}

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
				// Update the Terraform variables of the first version
				{
					Config: cfg6.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 5),
					),
				},
			},
		})
	})

	t.Run("AutoGenNameUpdateTFVars", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template3"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   ptr.Ref("name"),
							Value: ptr.Ref("world"),
						},
					},
					Active: ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[0].TerraformVariables = []testAccTemplateKeyValueConfig{
			{
				Key:   ptr.Ref("name"),
				Value: ptr.Ref("world2"),
			},
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
					),
				},
			},
		})
	})

	t.Run("CreateWithNoActiveVersionErrors", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(false),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg1.String(t),
					ExpectError: regexp.MustCompile("At least one template version must be active when creating"),
				},
			},
		})
	})

	t.Run("AmbiguousActiveVersionResolvedByModifying", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[0].Active = ptr.Ref(false)

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions[0].Directory = &exTemplateTwo

		cfg2b := cfg1
		cfg2b.Versions = slices.Clone(cfg2b.Versions)
		cfg2b.Versions = append(cfg2b.Versions, testAccTemplateVersionConfig{
			Directory: &exTemplateTwo,
			Active:    ptr.Ref(false),
		})

		cfg3b := cfg2b
		cfg3b.Versions = slices.Clone(cfg3b.Versions)
		cfg3b.Versions[1].Active = ptr.Ref(true)

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				// With an unmodified version deactivated, it's not clear what
				// the active version should be.
				{
					Config:      cfg2.String(t),
					ExpectError: regexp.MustCompile("Plan could not determine which version should be active."),
				},
				// If we modify the version, a new version will be created on `coderd`,
				// and the old version can remain active.
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("false"),
						}),
					),
				},
			},
		})
	})

	t.Run("AmbiguousActiveVersionResolvedByCreatingNewVersion", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[0].Active = ptr.Ref(false)
		cfg2.Versions = append(cfg2.Versions, testAccTemplateVersionConfig{
			Directory: &exTemplateTwo,
			Active:    ptr.Ref(false),
		})

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions[1].Active = ptr.Ref(true)

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				// Adding a new version that's not active doesn't help
				{
					Config:      cfg2.String(t),
					ExpectError: regexp.MustCompile("Plan could not determine which version should be active."),
				},
				// Making that new version active will fix the issue
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
					),
				},
			},
		})
	})

	t.Run("PushNewInactiveVersion", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = slices.Clone(cfg2.Versions)
		cfg2.Versions[0].Active = ptr.Ref(false)
		cfg2.Versions[0].Directory = &exTemplateTwo

		cfg3 := cfg2
		cfg3.Versions = slices.Clone(cfg3.Versions)
		cfg3.Versions[0].Active = ptr.Ref(true)

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Create one active version
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				// Modify an existing version, make it inactive
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("false"),
						}),
					),
				},
				// Make that modification active
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						testAccCheckNumTemplateVersions(ctx, client, 2),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
							"active": regexp.MustCompile("true"),
						}),
					),
				},
			},
		})
	})
}

func TestAccTemplateResourceEnterprise(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_acc", true)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	group, err := client.CreateGroup(ctx, firstUser.OrganizationIDs[0], codersdk.CreateGroupRequest{
		Name:           "bosses",
		QuotaAllowance: 200,
	})
	require.NoError(t, err)

	exTemplateOne := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	t.Run("BasicUsage", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			ACL: testAccTemplateACLConfig{
				GroupACL: []testAccTemplateKeyValueConfig{
					{
						Key:   ptr.Ref(firstUser.OrganizationIDs[0].String()),
						Value: ptr.Ref("use"),
					},
					{
						Key:   ptr.Ref(group.ID.String()),
						Value: ptr.Ref("admin"),
					},
				},
				UserACL: []testAccTemplateKeyValueConfig{
					{
						Key:   ptr.Ref(firstUser.ID.String()),
						Value: ptr.Ref("admin"),
					},
				},
			},
		}

		cfg2 := cfg1
		cfg2.ACL.GroupACL = slices.Clone(cfg2.ACL.GroupACL[1:])
		cfg2.MaxPortShareLevel = ptr.Ref("owner")

		cfg3 := cfg2
		cfg3.ACL.null = true
		cfg3.MaxPortShareLevel = ptr.Ref("public")

		cfg4 := cfg3
		cfg4.AllowUserAutostart = ptr.Ref(false)
		cfg4.AutostopRequirement = testAccAutostopRequirementConfig{
			DaysOfWeek: ptr.Ref([]string{"monday", "tuesday"}),
			Weeks:      ptr.Ref(int64(2)),
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg1.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "owner"),
						resource.TestCheckResourceAttr("coderd_template.test", "acl.groups.#", "2"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "acl.groups.*", map[string]*regexp.Regexp{
							"id":   regexp.MustCompile(firstUser.OrganizationIDs[0].String()),
							"role": regexp.MustCompile("^use$"),
						}),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "acl.groups.*", map[string]*regexp.Regexp{
							"id":   regexp.MustCompile(group.ID.String()),
							"role": regexp.MustCompile("^admin$"),
						}),
						resource.TestCheckResourceAttr("coderd_template.test", "acl.users.#", "1"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "acl.users.*", map[string]*regexp.Regexp{
							"id":   regexp.MustCompile(firstUser.ID.String()),
							"role": regexp.MustCompile("^admin$"),
						}),
					),
				},
				{
					Config: cfg2.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "owner"),
						resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "acl.users.*", map[string]*regexp.Regexp{
							"id":   regexp.MustCompile(firstUser.ID.String()),
							"role": regexp.MustCompile("^admin$"),
						}),
					),
				},
				{
					Config: cfg3.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "public"),
						resource.TestCheckNoResourceAttr("coderd_template.test", "acl"),
						func(s *terraform.State) error {
							templates, err := client.Templates(ctx, codersdk.TemplateFilter{})
							if err != nil {
								return err
							}
							if len(templates) != 1 {
								return fmt.Errorf("expected 1 template, got %d", len(templates))
							}
							acl, err := client.TemplateACL(ctx, templates[0].ID)
							if err != nil {
								return err
							}
							if len(acl.Groups) != 1 {
								return fmt.Errorf("expected 1 group ACL, got %d", len(acl.Groups))
							}
							if acl.Groups[0].Role != "admin" && acl.Groups[0].ID != group.ID {
								return fmt.Errorf("expected group ACL to be 'use' for %s, got %s", firstUser.OrganizationIDs[0].String(), acl.Groups[0].Role)
							}
							if len(acl.Users) != 1 {
								return fmt.Errorf("expected 1 user ACL, got %d", len(acl.Users))
							}
							if acl.Users[0].Role != "admin" && acl.Users[0].ID != firstUser.ID {
								return fmt.Errorf("expected user ACL to be 'admin' for %s, got %s", firstUser.ID.String(), acl.Users[0].Role)
							}
							return nil
						},
					),
				},
				{
					Config: cfg4.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "allow_user_auto_start", "false"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.days_of_week.#", "2"),
						resource.TestCheckResourceAttr("coderd_template.test", "auto_stop_requirement.weeks", "2"),
					),
				},
			},
		})
	})

	// Verifies that when `max_port_share_level` is set to to the default value,
	// an update request that would return HTTP Not Modified is not sent.
	t.Run("DefaultMaxPortShareLevel", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref("example-template"),
			Versions: []testAccTemplateVersionConfig{
				{
					Directory: &exTemplateOne,
					Active:    ptr.Ref(true),
				},
			},
			MaxPortShareLevel: ptr.Ref("owner"),
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg1.String(t),
					Check:  resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "owner"),
				},
			},
		})
	})
}

func TestAccTemplateResourceAGPL(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_agpl_acc", false)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	exTemplateOne := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg1 := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  ptr.Ref("example-template"),
		Versions: []testAccTemplateVersionConfig{
			{
				// Auto-generated version name
				Directory: &exTemplateOne,
				Active:    ptr.Ref(true),
			},
		},
		AllowUserAutostart: ptr.Ref(false),
	}

	cfg2 := cfg1
	cfg2.AllowUserAutostart = nil
	cfg2.AutostopRequirement.DaysOfWeek = ptr.Ref([]string{"monday", "tuesday"})

	cfg3 := cfg2
	cfg3.AutostopRequirement.null = true
	cfg3.AutostartRequirement = ptr.Ref([]string{})

	cfg4 := cfg3
	cfg4.FailureTTL = ptr.Ref(int64(1))

	cfg5 := cfg4
	cfg5.FailureTTL = nil
	cfg5.AutostartRequirement = nil
	cfg5.RequireActiveVersion = ptr.Ref(true)

	cfg6 := cfg5
	cfg6.RequireActiveVersion = nil
	cfg6.ACL = testAccTemplateACLConfig{
		GroupACL: []testAccTemplateKeyValueConfig{
			{
				Key:   ptr.Ref(firstUser.OrganizationIDs[0].String()),
				Value: ptr.Ref("use"),
			},
		},
	}

	cfg7 := cfg6
	cfg7.ACL.null = true
	cfg7.MaxPortShareLevel = ptr.Ref("owner")

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
			{
				Config:      cfg7.String(t),
				ExpectError: regexp.MustCompile("Your license is not entitled to use port sharing control"),
			},
		},
	})
}

func TestAccTemplateResourceVariables(t *testing.T) {
	t.Parallel()
	cfg := `
provider coderd {
	url   = %q
	token = %q
}

data "coderd_organization" "default" {
  is_default = true
}

variable "PRIOR_GIT_COMMIT_SHA" {
  default = "abcdef"
}

variable "CURRENT_GIT_COMMIT_SHA" {
  default = "ghijkl"
}

variable "ACTIVE" {
  default = true
}

resource "coderd_template" "sample" {
  name                  = "example-template"
  versions = [
    {
      name = "${var.PRIOR_GIT_COMMIT_SHA}"
      directory = %q
      active    = var.ACTIVE
    },
    {
      name = "${var.CURRENT_GIT_COMMIT_SHA}"
      directory = %q
      active    = false
    }
  ]
}`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_variables_acc", false)

	exTemplateOne := t.TempDir()
	err := cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg = fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), exTemplateOne, exTemplateOne)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
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
	MaxPortShareLevel            *string
	UseClassicParameterFlow      *bool

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
	max_port_share_level              = {{orNull .MaxPortShareLevel}}
	use_classic_parameter_flow        = {{orNull .UseClassicParameterFlow}}

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
	t.Parallel()
	aUUID := uuid.New()
	bUUID := uuid.New()
	cases := []struct {
		Name                string
		planVersions        Versions
		configVersions      Versions
		inputState          LastVersionsByHash
		expectedVersions    Versions
		cfgHasActiveVersion bool
		expectError         bool
	}{
		{
			Name: "IdenticalDontRename",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
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
						ID:     aUUID,
						Name:   "bar",
						TFVars: map[string]string{},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: []Variable{},
				},
			},
		},
		{
			Name: "IdenticalRenameFirst",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
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
						ID:     aUUID,
						Name:   "baz",
						TFVars: map[string]string{},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
			},
		},
		{
			Name: "IdenticalHashesInState",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
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
						ID:     aUUID,
						Name:   "qux",
						TFVars: map[string]string{},
					},
					{
						ID:     bUUID,
						Name:   "baz",
						TFVars: map[string]string{},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(bUUID),
					TerraformVariables: []Variable{},
				},
			},
		},
		{
			Name: "UnknownUsesStateInOrder",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringUnknown(),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
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
						ID:     aUUID,
						Name:   "qux",
						TFVars: map[string]string{},
					},
					{
						ID:     bUUID,
						Name:   "baz",
						TFVars: map[string]string{},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: []Variable{},
				},
				{
					Name:               types.StringValue("baz"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(bUUID),
					TerraformVariables: []Variable{},
				},
			},
		},
		{
			Name: "NewVersionNewRandomName",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("weird_draught12"),
					DirectoryHash:      types.StringValue("bbb"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: []Variable{},
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
						ID:     aUUID,
						Name:   "weird_draught12",
						TFVars: map[string]string{},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringUnknown(),
					DirectoryHash:      types.StringValue("bbb"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
				},
			},
		},
		{
			Name: "IdenticalNewVars",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
					TerraformVariables: []Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					},
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "foo",
						TFVars: map[string]string{
							"foo": "foo",
						},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            NewUUIDUnknown(),
					TerraformVariables: []Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					},
				},
			},
		},
		{
			Name: "IdenticalSameVars",
			planVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
					TerraformVariables: []Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					},
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:   aUUID,
						Name: "foo",
						TFVars: map[string]string{
							"foo": "bar",
						},
					},
				},
			},
			expectedVersions: []TemplateVersion{
				{
					Name:          types.StringValue("foo"),
					DirectoryHash: types.StringValue("aaa"),
					ID:            UUIDValue(aUUID),
					TerraformVariables: []Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					},
				},
			},
		},
		{
			Name: "NoPossibleActiveVersion",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: []Variable{},
					Active:             types.BoolValue(false),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
			},
			inputState: map[string][]PreviousTemplateVersion{
				"aaa": {
					{
						ID:     aUUID,
						Name:   "foo",
						TFVars: map[string]string{},
						Active: true,
					},
				},
			},
			cfgHasActiveVersion: false,
			expectError:         true,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()

			diag := c.planVersions.reconcileVersionIDs(c.inputState, c.configVersions, c.cfgHasActiveVersion)
			if c.expectError {
				require.True(t, diag.HasError())
			} else {
				require.Equal(t, c.expectedVersions, c.planVersions)
			}
		})

	}
}
