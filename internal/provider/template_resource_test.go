package provider

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/coder/v2/provisionersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	cp "github.com/otiai10/copy"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

// mustVariablesToSet converts a []Variable to a types.Set for use in tests,
// panicking if the conversion fails.
func mustVariablesToSet(vars []Variable) types.Set {
	s, diags := variablesToSet(context.Background(), vars)
	if diags.HasError() {
		panic(fmt.Sprintf("mustVariablesToSet: %v", diags.Errors()))
	}
	return s
}

func TestTemplateResourceReconcileVersionedMetadata(t *testing.T) {
	t.Parallel()

	t.Run("overwrites all versioned fields", func(t *testing.T) {
		t.Parallel()

		state := TemplateResourceModel{
			MaxPortShareLevel:       types.StringValue("owner"),
			CORSBehavior:            types.StringValue("simple"),
			UseClassicParameterFlow: types.BoolValue(true),
			AgentsAllowed:           types.BoolValue(false),
		}
		state.reconcileVersionedMetadata(&codersdk.Template{
			MaxPortShareLevel:       codersdk.WorkspaceAgentPortShareLevelPublic,
			CORSBehavior:            codersdk.CORSBehaviorPassthru,
			UseClassicParameterFlow: false,
			AgentsAllowed:           true,
		})

		require.Equal(t, "public", state.MaxPortShareLevel.ValueString())
		require.Equal(t, "passthru", state.CORSBehavior.ValueString())
		require.False(t, state.UseClassicParameterFlow.ValueBool())
		require.True(t, state.AgentsAllowed.ValueBool())
	})

	t.Run("empty CORS behavior", func(t *testing.T) {
		t.Parallel()

		state := TemplateResourceModel{CORSBehavior: types.StringValue("simple")}
		state.reconcileVersionedMetadata(&codersdk.Template{})

		require.True(t, state.CORSBehavior.IsNull())
	})
}

func TestTemplateResourceBoolRequests(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                    string
		useClassicParameterFlow types.Bool
		agentsAllowed           types.Bool
		wantClassic             *bool
		wantAgents              *bool
	}{
		{
			name:                    "classic unknown, agents true",
			useClassicParameterFlow: types.BoolUnknown(),
			agentsAllowed:           types.BoolValue(true),
			wantAgents:              new(true),
		},
		{
			name:                    "classic true, agents unknown",
			useClassicParameterFlow: types.BoolValue(true),
			agentsAllowed:           types.BoolUnknown(),
			wantClassic:             new(true),
		},
		{
			name:                    "classic null, agents false",
			useClassicParameterFlow: types.BoolNull(),
			agentsAllowed:           types.BoolValue(false),
			wantAgents:              new(false),
		},
		{
			name:                    "classic false, agents null",
			useClassicParameterFlow: types.BoolValue(false),
			agentsAllowed:           types.BoolNull(),
			wantClassic:             new(false),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			model := TemplateResourceModel{
				AutostopRequirement: types.ObjectValueMust(autostopRequirementTypeAttr, map[string]attr.Value{
					"days_of_week": types.SetValueMust(types.StringType, []attr.Value{}),
					"weeks":        types.Int64Value(1),
				}),
				AutostartPermittedDaysOfWeek: types.SetValueMust(types.StringType, []attr.Value{}),
				UseClassicParameterFlow:      tc.useClassicParameterFlow,
				AgentsAllowed:                tc.agentsAllowed,
				ACL:                          types.ObjectNull(aclTypeAttr),
			}

			var updateDiags diag.Diagnostics
			updateReq := model.toUpdateRequest(t.Context(), &updateDiags)
			require.False(t, updateDiags.HasError(), updateDiags.Errors())
			require.Equal(t, tc.wantClassic, updateReq.UseClassicParameterFlow)
			require.Equal(t, tc.wantAgents, updateReq.AgentsAllowed)

			createResp := frameworkresource.CreateResponse{}
			createReq := model.toCreateRequest(t.Context(), &createResp, uuid.New())
			require.False(t, createResp.Diagnostics.HasError(), createResp.Diagnostics.Errors())
			require.Equal(t, tc.wantClassic, createReq.UseClassicParameterFlow)
			require.Equal(t, tc.wantAgents, createReq.AgentsAllowed)
		})
	}
}

func TestTemplateResourceACLRoleSchemaValidation(t *testing.T) {
	t.Parallel()

	directory := "."
	cfg := testAccTemplateResourceConfig{
		URL:   "http://127.0.0.1",
		Token: "test-token",
		Name:  new("example-template"),
		Versions: new([]testAccTemplateVersionConfig{
			{
				Directory: &directory,
				Active:    new(true),
			},
		}),
		ACL: testAccTemplateACLConfig{
			GroupACL: []testAccTemplateKeyValueConfig{
				{
					Key:   new("00000000-0000-0000-0000-000000000000"),
					Value: new("owner"),
				},
			},
		},
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg.String(t),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}

func TestTemplateResourceDescriptionSchemaValidation(t *testing.T) {
	t.Parallel()

	directory := "."
	cfg := testAccTemplateResourceConfig{
		URL:         "http://127.0.0.1",
		Token:       "test-token",
		Name:        new("example-template"),
		Description: new(strings.Repeat("a", 128)),
		Versions: new([]testAccTemplateVersionConfig{
			{
				Directory: &directory,
				Active:    new(true),
			},
		}),
		ACL: testAccTemplateACLConfig{
			null: true,
		},
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      cfg.String(t),
				ExpectError: regexp.MustCompile(`must be at most 127`),
			},
		},
	})
}

func TestAccTemplateResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_acc")
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
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		cfg2.Name = new("example-template-new")
		(*cfg2.Versions)[0].Directory = &exTemplateTwo
		(*cfg2.Versions)[0].Name = new("new")

		cfg3 := cfg2
		cfg3.Versions = new(slices.Clone(*cfg3.Versions))
		cfg3.Versions = new(append(*cfg3.Versions, testAccTemplateVersionConfig{
			Name:      new("legacy-template"),
			Directory: &exTemplateOne,
			Active:    new(false),
			TerraformVariables: []testAccTemplateKeyValueConfig{
				{
					Key:   new("name"),
					Value: new("world"),
				},
			},
		}))

		cfg4 := cfg3
		cfg4.Versions = new(slices.Clone(*cfg4.Versions))
		(*cfg4.Versions)[0].Active = new(false)
		(*cfg4.Versions)[1].Active = new(true)

		cfg5 := cfg4
		cfg5.Versions = new(slices.Clone(*cfg5.Versions))
		(*cfg5.Versions)[0], (*cfg5.Versions)[1] = (*cfg5.Versions)[1], (*cfg5.Versions)[0]

		cfg6 := cfg4
		cfg6.Versions = new(slices.Clone((*cfg6.Versions)[1:]))

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
						file := fmt.Sprintf("%s/terraform.tfvars", *(*cfg1.Versions)[0].Directory)
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
						file := fmt.Sprintf("%s/terraform.tfvars", *(*cfg1.Versions)[0].Directory)
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
			Name:  new("example-template2"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   new("name"),
							Value: new("world"),
						},
					},
					Active: new(true),
				},
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   new("name"),
							Value: new("world"),
						},
					},
					Active: new(false),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		(*cfg2.Versions)[1].Name = new("new-name")

		cfg3 := cfg2
		cfg3.Versions = new(slices.Clone(*cfg3.Versions))
		(*cfg3.Versions)[0].Name = new("new-name-one")
		(*cfg3.Versions)[1].Name = new("new-name-two")
		(*cfg3.Versions)[0], (*cfg3.Versions)[1] = (*cfg3.Versions)[1], (*cfg3.Versions)[0]

		cfg4 := cfg1
		cfg4.Versions = new(slices.Clone(*cfg4.Versions))
		(*cfg4.Versions)[0].Directory = &exTemplateOne

		cfg5 := cfg4
		cfg5.Versions = new(slices.Clone(*cfg5.Versions))
		(*cfg5.Versions)[1].Directory = &exTemplateOne

		cfg6 := cfg5
		cfg6.Versions = new(slices.Clone(*cfg6.Versions))
		(*cfg6.Versions)[0].TerraformVariables = []testAccTemplateKeyValueConfig{
			{
				Key:   new("name"),
				Value: new("world2"),
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
			Name:  new("example-template3"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateTwo,
					TerraformVariables: []testAccTemplateKeyValueConfig{
						{
							Key:   new("name"),
							Value: new("world"),
						},
					},
					Active: new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		(*cfg2.Versions)[0].TerraformVariables = []testAccTemplateKeyValueConfig{
			{
				Key:   new("name"),
				Value: new("world2"),
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
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(false),
				},
			}),
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
					Config: cfg1.String(t),
					// PlanOnly asserts this is caught by the versions plan
					// modifier during `terraform plan`, not deferred to
					// `terraform apply`
					PlanOnly:    true,
					ExpectError: regexp.MustCompile("At least one template version must be active when creating"),
				},
			},
		})
	})

	t.Run("AmbiguousActiveVersionResolvedByModifying", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		(*cfg2.Versions)[0].Active = new(false)

		cfg3 := cfg2
		cfg3.Versions = new(slices.Clone(*cfg3.Versions))
		(*cfg3.Versions)[0].Directory = &exTemplateTwo

		cfg2b := cfg1
		cfg2b.Versions = new(slices.Clone(*cfg2b.Versions))
		cfg2b.Versions = new(append(*cfg2b.Versions, testAccTemplateVersionConfig{
			Directory: &exTemplateTwo,
			Active:    new(false),
		}))

		cfg3b := cfg2b
		cfg3b.Versions = new(slices.Clone(*cfg3b.Versions))
		(*cfg3b.Versions)[1].Active = new(true)

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
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		(*cfg2.Versions)[0].Active = new(false)
		cfg2.Versions = new(append(*cfg2.Versions, testAccTemplateVersionConfig{
			Directory: &exTemplateTwo,
			Active:    new(false),
		}))

		cfg3 := cfg2
		cfg3.Versions = new(slices.Clone(*cfg3.Versions))
		(*cfg3.Versions)[1].Active = new(true)

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
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				null: true,
			},
		}

		cfg2 := cfg1
		cfg2.Versions = new(slices.Clone(*cfg2.Versions))
		(*cfg2.Versions)[0].Active = new(false)
		(*cfg2.Versions)[0].Directory = &exTemplateTwo

		cfg3 := cfg2
		cfg3.Versions = new(slices.Clone(*cfg3.Versions))
		(*cfg3.Versions)[0].Active = new(true)

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

	t.Run("InvalidMaxPortShareLevel", func(t *testing.T) {
		cfg1 := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL:               testAccTemplateACLConfig{null: true},
			MaxPortShareLevel: new("invalid"),
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg1.String(t),
					ExpectError: regexp.MustCompile(`value must be one of`),
				},
			},
		})
	})
}

func TestAccTemplateResourceAgentsAllowed(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_agents_allowed_acc")
	buildInfo, err := client.BuildInfo(ctx)
	require.NoError(t, err, "fetch buildinfo")
	if semver.Compare(buildInfo.CanonicalVersion(), "v"+templateAgentsAllowedMinVersion) < 0 {
		t.Skipf("test requires Coder v%s or later, deployment is %s", templateAgentsAllowedMinVersion, buildInfo.CanonicalVersion())
	}

	directory := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template", directory)
	require.NoError(t, err)

	cfgOmitted := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  new("agents-allowed-template"),
		Versions: new([]testAccTemplateVersionConfig{
			{
				Directory: &directory,
				Active:    new(true),
			},
		}),
		ACL: testAccTemplateACLConfig{null: true},
	}
	cfgFalse := cfgOmitted
	cfgFalse.AgentsAllowed = new(false)
	cfgTrue := cfgFalse
	cfgTrue.AgentsAllowed = new(true)
	cfgOmittedAgain := cfgTrue
	cfgOmittedAgain.AgentsAllowed = nil

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfgOmitted.String(t),
				Check:  resource.TestCheckResourceAttr("coderd_template.test", "agents_allowed", "true"),
			},
			{
				Config: cfgFalse.String(t),
				Check:  resource.TestCheckResourceAttr("coderd_template.test", "agents_allowed", "false"),
			},
			{
				Config: cfgTrue.String(t),
				Check:  resource.TestCheckResourceAttr("coderd_template.test", "agents_allowed", "true"),
			},
			{
				// Omitting the attribute again preserves the prior state value.
				Config:   cfgOmittedAgain.String(t),
				PlanOnly: true,
			},
		},
	})
}

// TestAccTemplateResourceOptionalVersions covers PLAT-288: `versions` is
// optional, so `coderd_template` can manage a template's settings without
// owning its version lifecycle. A template still can't be *created* without
// at least one version (Coderd has no concept of a versionless template), so
// that case is expected to keep failing with a clear error.
func TestAccTemplateResourceOptionalVersions(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_optional_versions_acc")

	exTemplate := t.TempDir()
	err := cp.Copy("../../integration/template-test/example-template", exTemplate)
	require.NoError(t, err)

	t.Run("CreateWithoutVersionsErrors", func(t *testing.T) {
		cfg := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new("no-versions-template"),
			ACL:   testAccTemplateACLConfig{null: true},
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:   cfg.String(t),
					PlanOnly: true,
					// Terraform's CLI diagnostic renderer word-wraps long error
					// text with real newlines, so `.` must match them too.
					ExpectError: regexp.MustCompile("(?s)At least one template version.*is required when.*creating"),
				},
			},
		})
	})

	t.Run("ImportThenPlanWithoutActiveVersionDoesNotError", func(t *testing.T) {
		firstUser, err := client.User(ctx, codersdk.Me)
		require.NoError(t, err)
		orgID := firstUser.OrganizationIDs[0]

		// Create the template directly via the API rather than through
		// Terraform: a resource.Test call destroys everything it created
		// once it returns, which would delete the template before the
		// import step below ever got to see it.
		version, _, err := newVersion(ctx, client, newVersionRequest{
			OrganizationID: orgID,
			Version: &TemplateVersion{
				Directory:          types.StringValue(exTemplate),
				TerraformVariables: emptyVariableSet(),
				ProvisionerTags:    emptyVariableSet(),
			},
		})
		require.NoError(t, err)
		tpl, err := client.CreateTemplate(ctx, orgID, codersdk.CreateTemplateRequest{
			Name:      "import-no-active-template",
			VersionID: version.ID,
		})
		require.NoError(t, err)

		cfgManaged := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new(tpl.Name),
			Versions: new([]testAccTemplateVersionConfig{
				{
					Directory: &exTemplate,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{null: true},
		}

		// Describes the same template post-import, without marking any
		// version active. The template already has an active version on
		// the server; this config is just adopting it, not creating it.
		cfgReimported := cfgManaged
		cfgReimported.Versions = new(slices.Clone(*cfgReimported.Versions))
		(*cfgReimported.Versions)[0].Active = new(false)

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Import into brand new state. `Create` never runs for this
				// resource instance, so there's no private-state record of
				// any tracked version -- exactly the scenario that broke the
				// old private-state-is-nil creation heuristic.
				{
					Config:             cfgManaged.String(t),
					ResourceName:       "coderd_template.test",
					ImportState:        true,
					ImportStateId:      tpl.ID.String(),
					ImportStatePersist: true,
				},
				// The first plan after import must not demand an active
				// version just because there's no tracked private state:
				// req.State.Raw.IsNull() (unlike the old private-state-is-nil
				// heuristic it replaced) correctly recognizes this isn't a
				// Create, since prior state already exists from the import.
				// A non-empty plan is expected, since Terraform is now
				// tracking this version for the first time.
				{
					Config:             cfgReimported.String(t),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})

	t.Run("SettingsOnlyManagementAfterDroppingVersions", func(t *testing.T) {
		firstUser, err := client.User(ctx, codersdk.Me)
		require.NoError(t, err)
		orgID := firstUser.OrganizationIDs[0]

		cfgManaged := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new("settings-only-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					Directory: &exTemplate,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{null: true},
		}

		cfgUnmanaged := cfgManaged
		cfgUnmanaged.Versions = nil

		cfgUnmanagedWithNewDescription := cfgUnmanaged
		cfgUnmanagedWithNewDescription.Description = new("now managed by an external pipeline")

		// Captured from state in the first step's Check, then used both to
		// simulate the pipeline's out-of-band push (PreConfig runs before a
		// step's plan/apply and has no access to *terraform.State, so the ID
		// has to be threaded through this way) and to verify against the
		// Coderd API directly in the last step.
		var templateID uuid.UUID
		var pipelineVersionID uuid.UUID
		captureTemplateID := func(s *terraform.State) error {
			rs, ok := s.RootModule().Resources["coderd_template.test"]
			if !ok {
				return fmt.Errorf("coderd_template.test not found in state")
			}
			id, err := uuid.Parse(rs.Primary.ID)
			if err != nil {
				return fmt.Errorf("failed to parse template id %q: %w", rs.Primary.ID, err)
			}
			templateID = id
			return nil
		}

		// Simulates the customer's own CI pipeline calling `coder templates
		// push --activate` outside of Terraform, after Terraform has stopped
		// tracking any version for this template.
		pushAndActivateExternalVersion := func() {
			version := TemplateVersion{
				Directory:          types.StringValue(exTemplate),
				TerraformVariables: emptyVariableSet(),
				ProvisionerTags:    emptyVariableSet(),
			}
			versionResp, logs, err := newVersion(ctx, client, newVersionRequest{
				Version:        &version,
				OrganizationID: orgID,
				TemplateID:     &templateID,
			})
			require.NoError(t, err, "%v", logs)
			require.NoError(t, markActive(ctx, client, templateID, versionResp.ID))
			pipelineVersionID = versionResp.ID
		}

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfgManaged.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
						resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "1"),
						testAccCheckNumTemplateVersions(ctx, client, 1),
						captureTemplateID,
					),
				},
				// The team switches to a custom pipeline for pushing versions;
				// Terraform is told to stop tracking versions entirely.
				{
					Config: cfgUnmanaged.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_template.test", "versions"),
						testAccCheckNumTemplateVersions(ctx, client, 1),
					),
				},
				// The pipeline pushes and activates a new version, entirely
				// outside Terraform. Terraform's config is unchanged, so the
				// next plan/apply should be a no-op: it isn't tracking any
				// version, so there's nothing to reconcile or fight over.
				{
					PreConfig: pushAndActivateExternalVersion,
					Config:    cfgUnmanaged.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_template.test", "versions"),
						testAccCheckNumTemplateVersions(ctx, client, 2),
					),
				},
				// Terraform updates a setting. Only the setting should change:
				// the pipeline's active version must survive untouched.
				{
					Config: cfgUnmanagedWithNewDescription.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr("coderd_template.test", "description", "now managed by an external pipeline"),
						resource.TestCheckNoResourceAttr("coderd_template.test", "versions"),
						testAccCheckNumTemplateVersions(ctx, client, 2),
						func(*terraform.State) error {
							tmpl, err := client.Template(ctx, templateID)
							if err != nil {
								return err
							}
							if tmpl.ActiveVersionID != pipelineVersionID {
								return fmt.Errorf("expected pipeline-pushed version %s to remain active, got %s", pipelineVersionID, tmpl.ActiveVersionID)
							}
							return nil
						},
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
	client := integration.StartCoder(ctx, t, "template_resource_acc", integration.UseLicense)
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
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					// Auto-generated version name
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
			ACL: testAccTemplateACLConfig{
				GroupACL: []testAccTemplateKeyValueConfig{
					{
						Key:   new(firstUser.OrganizationIDs[0].String()),
						Value: new("use"),
					},
					{
						Key:   new(group.ID.String()),
						Value: new("admin"),
					},
				},
				UserACL: []testAccTemplateKeyValueConfig{
					{
						Key:   new(firstUser.ID.String()),
						Value: new("admin"),
					},
				},
			},
		}

		cfg2 := cfg1
		cfg2.ACL.GroupACL = slices.Clone(cfg2.ACL.GroupACL[1:])
		cfg2.MaxPortShareLevel = new("owner")
		cfg2.CORSBehavior = new("passthru")

		cfg3 := cfg2
		cfg3.ACL.null = true
		cfg3.MaxPortShareLevel = new("public")
		cfg3.CORSBehavior = new("simple")

		cfg4 := cfg3
		cfg4.AllowUserAutostart = new(false)
		cfg4.AutostopRequirement = testAccAutostopRequirementConfig{
			DaysOfWeek: new([]string{"monday", "tuesday"}),
			Weeks:      new(int64(2)),
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
						resource.TestCheckResourceAttr("coderd_template.test", "cors_behavior", "simple"),
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
						resource.TestCheckResourceAttr("coderd_template.test", "cors_behavior", "passthru"),
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
						resource.TestCheckResourceAttr("coderd_template.test", "cors_behavior", "simple"),
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

	// Verifies that all valid max_port_share_level constants are accepted and
	// round-trip correctly through the API, including updates between values.
	t.Run("MaxPortShareLevelConstants", func(t *testing.T) {
		baseCfg := testAccTemplateResourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  new("example-template"),
			Versions: new([]testAccTemplateVersionConfig{
				{
					Directory: &exTemplateOne,
					Active:    new(true),
				},
			}),
		}

		cfgOwner := baseCfg
		cfgOwner.MaxPortShareLevel = new("owner")

		cfgAuthenticated := baseCfg
		cfgAuthenticated.MaxPortShareLevel = new("authenticated")

		cfgOrganization := baseCfg
		cfgOrganization.MaxPortShareLevel = new("organization")

		cfgPublic := baseCfg
		cfgPublic.MaxPortShareLevel = new("public")

		resource.Test(t, resource.TestCase{
			PreCheck:                 func() { testAccPreCheck(t) },
			IsUnitTest:               true,
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfgOwner.String(t),
					Check:  resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "owner"),
				},
				{
					Config: cfgAuthenticated.String(t),
					Check:  resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "authenticated"),
				},
				{
					Config: cfgOrganization.String(t),
					Check:  resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "organization"),
				},
				{
					Config: cfgPublic.String(t),
					Check:  resource.TestCheckResourceAttr("coderd_template.test", "max_port_share_level", "public"),
				},
			},
		})
	})
}

func TestAccTemplateResourceBackCompat(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	// Coder 2.25 does not support cors_behavior. Verify that not setting it works.
	client := integration.StartCoder(ctx, t, "tmpl_back_compat_acc", integration.CoderVersion("v2.25.0"))

	exTemplateOne := t.TempDir()
	err := cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg1 := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  new("example-template"),
		Versions: new([]testAccTemplateVersionConfig{
			{
				Directory: &exTemplateOne,
				Active:    new(true),
			},
		}),
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
				Config: cfg1.String(t),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "id"),
					resource.TestCheckNoResourceAttr("coderd_template.test", "cors_behavior"),
				),
			},
		},
	})
}

func TestAccTemplateResourceAGPL(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_agpl_acc")
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	organizationID := firstUser.OrganizationIDs[0].String()

	exTemplateOne := t.TempDir()
	err = cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg1 := testAccTemplateResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
		Name:  new("example-template"),
		Versions: new([]testAccTemplateVersionConfig{
			{
				// Auto-generated version name
				Directory: &exTemplateOne,
				Active:    new(true),
			},
		}),
		AllowUserAutostart: new(false),
	}

	cfg2 := cfg1
	cfg2.AllowUserAutostart = nil
	cfg2.AutostopRequirement.DaysOfWeek = new([]string{"monday", "tuesday"})

	cfg3 := cfg2
	cfg3.AutostopRequirement.null = true
	cfg3.AutostartRequirement = new([]string{})

	cfg4 := cfg3
	cfg4.FailureTTL = new(int64(1))

	cfg5 := cfg4
	cfg5.FailureTTL = nil
	cfg5.AutostartRequirement = nil
	cfg5.RequireActiveVersion = new(true)

	cfg6 := cfg5
	cfg6.RequireActiveVersion = nil
	cfg6.ACL = testAccTemplateACLConfig{
		GroupACL: []testAccTemplateKeyValueConfig{
			{
				Key:   &organizationID,
				Value: new("use"),
			},
		},
	}

	cfg7 := cfg6
	cfg7.ACL.null = true
	cfg7.MaxPortShareLevel = new("owner")

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
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
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
	client := integration.StartCoder(ctx, t, "template_resource_variables_acc")

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

func TestAccTemplateResourceSensitiveTFVarsDeferredReplan(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	// Changing the sensitive tf_var forces random_uuid to be replaced, which
	// makes the version name unknown during planning and triggers a deferred
	// re-plan during apply. Before PlanModifyList started patching the original
	// attr.Values instead of rebuilding the whole list, this failed with:
	// "Provider produced inconsistent final plan ... inconsistent values for
	// sensitive attribute".
	cfg := `
provider coderd {
	url   = %q
	token = %q
}

variable "secret_one" {
	type      = string
	sensitive = true
	default   = %q
}

locals {
	my_secrets = {
		normal = "hi"
		secret = var.secret_one
	}
}

resource "random_uuid" "uuid" {
	keepers = local.my_secrets
}

resource "coderd_template" "test" {
	name = "sensitive-template"

	versions = [{
		name      = random_uuid.uuid.result
		directory = %q
		active    = true
		tf_vars = [for k, v in local.my_secrets : {
			name  = k
			value = tostring(v)
		}]
	}]
}
`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_sensitive_tfvars_acc")

	exTemplateOne := t.TempDir()
	err := cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg1 := fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), "no", exTemplateOne)
	cfg2 := fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), "yes", exTemplateOne)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		ExternalProviders: map[string]resource.ExternalProvider{
			"random": {
				Source:            "hashicorp/random",
				VersionConstraint: "~> 3.7",
			},
		},
		Steps: []resource.TestStep{
			{
				Config: cfg1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "1"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name":           regexp.MustCompile(".+"),
						"id":             regexp.MustCompile(".+"),
						"directory_hash": regexp.MustCompile(".+"),
					}),
					testAccCheckNumTemplateVersions(ctx, client, 1),
				),
			},
			{
				Config: cfg2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "1"),
					resource.TestMatchTypeSetElemNestedAttrs("coderd_template.test", "versions.*", map[string]*regexp.Regexp{
						"name":           regexp.MustCompile(".+"),
						"id":             regexp.MustCompile(".+"),
						"directory_hash": regexp.MustCompile(".+"),
					}),
					testAccCheckNumTemplateVersions(ctx, client, 2),
				),
			},
		},
	})
}

// TestAccTemplateResourceTFVarsFromVariable reproduces issue #305: tf_vars and
// provisioner_tags supplied through required input variables (the equivalent
// of `terraform plan -var-file=...`) are unknown while Terraform's validate
// walk runs at the start of plan and apply, because the validate walk
// evaluates input variables without values as unknown. With []Variable struct
// fields this failed with "Value Conversion Error ... Path: [0].tf_vars"
// before the provider was even configured. The variables must not have
// defaults; a default would make them known during the validate walk and skip
// the regression entirely. The two variables deliberately use different type
// constraints, list(object) and list(map(string)), to cover both input shapes
// from the original reports.
func TestAccTemplateResourceTFVarsFromVariable(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	cfg := `
provider coderd {
	url   = %q
	token = %q
}

variable "template_variables" {
	type = list(object({
		name = string,
		value = any
	}))
}

variable "provisioner_tags" {
	type = list(map(string))
}

resource "coderd_template" "test" {
	name = "tf-vars-from-variable"

	versions = [{
		directory        = %q
		active           = true
		tf_vars          = var.template_variables
		provisioner_tags = var.provisioner_tags
	}]
}
`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_tfvars_from_var_acc")

	exTemplateOne := t.TempDir()
	err := cp.Copy("../../integration/template-test/example-template", exTemplateOne)
	require.NoError(t, err)

	cfg = fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), exTemplateOne)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				ConfigVariables: config.Variables{
					"template_variables": config.ListVariable(
						config.MapVariable(map[string]config.Variable{
							"name":  config.StringVariable("name"),
							"value": config.StringVariable("world"),
						}),
					),
					"provisioner_tags": config.ListVariable(),
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.#", "1"),
					resource.TestCheckResourceAttr("coderd_template.test", "versions.0.tf_vars.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("coderd_template.test", "versions.0.tf_vars.*", map[string]string{
						"name":  "name",
						"value": "world",
					}),
					testAccCheckNumTemplateVersions(ctx, client, 1),
				),
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
	CORSBehavior                 *string
	UseClassicParameterFlow      *bool
	AgentsAllowed                *bool

	// Versions is a pointer so that a nil value renders `versions = null`
	// (matching AutostartRequirement above), letting tests exercise
	// settings-only management of a template (see PLAT-288).
	Versions *[]testAccTemplateVersionConfig
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

func (c testAccTemplateResourceConfig) versionsString(t *testing.T) string {
	t.Helper()
	if c.Versions == nil {
		return "null"
	}
	tpl := `[
	{{- range . }}
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
	`

	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("versions").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, *c.Versions)
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
	cors_behavior                     = {{orNull .CORSBehavior}}
	use_classic_parameter_flow        = {{orNull .UseClassicParameterFlow}}
	agents_allowed                    = {{orNull .AgentsAllowed}}

	acl = ` + c.ACL.String(t) + `

	versions = ` + c.versionsString(t) + `
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("bar"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(bUUID),
					TerraformVariables: emptyVariableSet(),
				},
			},
		},
		{
			// Config name is null (auto-generated), plan name is unknown.
			// Should backfill name from state since the user didn't set one.
			Name: "UnknownUsesStateInOrder",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringUnknown(),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
				},
			},
			configVersions: []TemplateVersion{
				{
					Name: types.StringValue("foo"),
				},
				{
					Name: types.StringNull(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringValue("baz"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(bUUID),
					TerraformVariables: emptyVariableSet(),
				},
			},
		},
		{
			// Config name is non-null (e.g. random_uuid.result), plan name is unknown
			// because the upstream resource is being recreated.
			// Should NOT backfill name — leave it unknown to resolve after apply.
			Name: "UnknownNonNullConfigNameNotBackfilled",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringUnknown(),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
				},
				{
					Name:               types.StringUnknown(),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(bUUID),
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: emptyVariableSet(),
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
					TerraformVariables: mustVariablesToSet([]Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					}),
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
					TerraformVariables: mustVariablesToSet([]Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					}),
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
					TerraformVariables: mustVariablesToSet([]Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					}),
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
					TerraformVariables: mustVariablesToSet([]Variable{
						{
							Name:  types.StringValue("foo"),
							Value: types.StringValue("bar"),
						},
					}),
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
					TerraformVariables: emptyVariableSet(),
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
		{
			Name: "UnknownTFVarsHandled",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: types.SetUnknown(variableSetElemType),
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
						TFVars: map[string]string{"x": "y"},
					},
				},
			},
			// An unknown tf_vars set cannot be compared against the prior state,
			// so a new version is created (ID becomes unknown).
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 NewUUIDUnknown(),
					TerraformVariables: types.SetUnknown(variableSetElemType),
				},
			},
		},
		{
			Name: "NullTFVarsHandled",
			planVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: types.SetNull(variableSetElemType),
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
					},
				},
			},
			// A null tf_vars set is treated as no variables, so the existing
			// version is reused without error.
			expectedVersions: []TemplateVersion{
				{
					Name:               types.StringValue("foo"),
					DirectoryHash:      types.StringValue("aaa"),
					ID:                 UUIDValue(aUUID),
					TerraformVariables: types.SetNull(variableSetElemType),
				},
			},
		},
	}

	for _, c := range cases {
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

// versionObjectType returns the attr.Type for a single element of the versions
// list attribute, matching the resource schema. It is shared by the regression
// tests for issue #305.
func versionObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"id":        UUIDType,
			"name":      types.StringType,
			"message":   types.StringType,
			"directory": types.StringType,
			"files": types.MapType{
				ElemType: types.StringType,
			},
			"archive_base64": types.StringType,
			"archive_file":   types.StringType,
			"directory_hash": types.StringType,
			"active":         types.BoolType,
			"tf_vars": types.SetType{
				ElemType: variableSetElemType,
			},
			"provisioner_tags": types.SetType{
				ElemType: variableSetElemType,
			},
		},
	}
}

// TestValidateListUnknownTFVars reproduces issue #305: when tf_vars or
// provisioner_tags are derived from variables that are unknown at validate
// time, ValidateResourceConfig invokes the list validator with an unknown set
// element. With []Variable fields ElementsAs could not decode an unknown set
// into a native slice and returned a "Value Conversion Error". With types.Set
// fields the validator must succeed without diagnostics.
func TestValidateListUnknownTFVars(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	objType := versionObjectType()

	versionVal, diags := types.ObjectValue(objType.AttrTypes, map[string]attr.Value{
		"id":               NewUUIDUnknown(),
		"name":             types.StringValue("main"),
		"message":          types.StringValue(""),
		"directory":        types.StringValue("./template"),
		"files":            types.MapNull(types.StringType),
		"archive_base64":   types.StringNull(),
		"archive_file":     types.StringNull(),
		"directory_hash":   types.StringUnknown(),
		"active":           types.BoolValue(true),
		"tf_vars":          types.SetUnknown(variableSetElemType),
		"provisioner_tags": types.SetUnknown(variableSetElemType),
	})
	require.False(t, diags.HasError(), "building version object: %v", diags.Errors())

	listVal, diags := types.ListValue(objType, []attr.Value{versionVal})
	require.False(t, diags.HasError(), "building list: %v", diags.Errors())

	resp := &validator.ListResponse{}
	NewVersionsValidator().ValidateList(ctx, validator.ListRequest{
		ConfigValue: listVal,
	}, resp)
	require.False(t, resp.Diagnostics.HasError(),
		"ValidateList must accept unknown tf_vars: %v", resp.Diagnostics.Errors())
}

// TestUnknownTFVarsDeserialization validates that a TemplateVersion can be
// decoded from a types.List even when tf_vars or provisioner_tags contain
// unknown or null values. This is the exact ElementsAs code path that failed
// in issue #305 when tf_vars came from a for-expression over variables with
// sensitive or unknown values.
func TestUnknownTFVarsDeserialization(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	objType := versionObjectType()

	// buildAndDeserialize wraps a single version object (with the given tf_vars
	// and provisioner_tags values) in a list and decodes it into
	// []TemplateVersion, asserting that ElementsAs succeeds.
	buildAndDeserialize := func(t *testing.T, tfVars, provTags attr.Value) []TemplateVersion {
		t.Helper()
		obj, d := types.ObjectValue(objType.AttrTypes, map[string]attr.Value{
			"id":               NewUUIDUnknown(),
			"name":             types.StringValue("stable-1"),
			"message":          types.StringValue(""),
			"directory":        types.StringValue("/tmp/test"),
			"files":            types.MapNull(types.StringType),
			"archive_base64":   types.StringNull(),
			"archive_file":     types.StringNull(),
			"directory_hash":   types.StringUnknown(),
			"active":           types.BoolValue(true),
			"tf_vars":          tfVars,
			"provisioner_tags": provTags,
		})
		require.False(t, d.HasError(), "building version object: %v", d.Errors())
		lv, d := types.ListValue(objType, []attr.Value{obj})
		require.False(t, d.HasError(), "building list: %v", d.Errors())
		var result []TemplateVersion
		d = lv.ElementsAs(ctx, &result, false)
		require.False(t, d.HasError(), "ElementsAs failed: %v", d.Errors())
		require.Len(t, result, 1)
		return result
	}

	knownVarSet := mustVariablesToSet([]Variable{
		{Name: types.StringValue("secret-1"), Value: types.StringValue("s3cret")},
		{Name: types.StringValue("normal-info-1"), Value: types.StringValue("hello")},
	})
	knownTagSet := mustVariablesToSet([]Variable{
		{Name: types.StringValue("scope"), Value: types.StringValue("org")},
	})
	unknownSet := types.SetUnknown(variableSetElemType)
	nullSet := types.SetNull(variableSetElemType)

	t.Run("BothUnknown", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, unknownSet, unknownSet)
		require.True(t, result[0].TerraformVariables.IsUnknown())
		require.True(t, result[0].ProvisionerTags.IsUnknown())
		require.Equal(t, "stable-1", result[0].Name.ValueString())
	})

	t.Run("BothKnown", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, knownVarSet, knownTagSet)
		require.False(t, result[0].TerraformVariables.IsUnknown())
		require.False(t, result[0].ProvisionerTags.IsUnknown())
		vars, d := variablesFromSet(ctx, result[0].TerraformVariables)
		require.False(t, d.HasError())
		require.Len(t, vars, 2)
		tags, d := variablesFromSet(ctx, result[0].ProvisionerTags)
		require.False(t, d.HasError())
		require.Len(t, tags, 1)
	})

	t.Run("TFVarsUnknown_TagsKnown", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, unknownSet, knownTagSet)
		require.True(t, result[0].TerraformVariables.IsUnknown())
		require.False(t, result[0].ProvisionerTags.IsUnknown())
	})

	t.Run("TFVarsNull_TagsUnknown", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, nullSet, unknownSet)
		require.True(t, result[0].TerraformVariables.IsNull())
		require.True(t, result[0].ProvisionerTags.IsUnknown())
	})

	t.Run("BothNull", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, nullSet, nullSet)
		require.True(t, result[0].TerraformVariables.IsNull())
		require.True(t, result[0].ProvisionerTags.IsNull())
	})

	t.Run("BothEmpty", func(t *testing.T) {
		t.Parallel()
		result := buildAndDeserialize(t, emptyVariableSet(), emptyVariableSet())
		require.False(t, result[0].TerraformVariables.IsNull())
		require.False(t, result[0].TerraformVariables.IsUnknown())
		vars, d := variablesFromSet(ctx, result[0].TerraformVariables)
		require.False(t, d.HasError())
		require.Empty(t, vars)
	})
}

// sortedPaths returns the paths of a file map in a deterministic order, so that
// the archives the helpers below build are reproducible.
func sortedPaths(files map[string]string) []string {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// mustTar builds an uncompressed tar archive from a map of file path to
// content.
func mustTar(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, path := range sortedPaths(files) {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     path,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(files[path])),
		}))
		_, err := tw.Write([]byte(files[path]))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// mustTarBase64 builds a base64-encoded tar archive from a map of file path to
// content, for use as a version's `archive_base64`.
func mustTarBase64(t *testing.T, files map[string]string) string {
	t.Helper()
	return base64.StdEncoding.EncodeToString(mustTar(t, files))
}

// mustGzip compresses an archive, as `tar -czf` or the `archive_file` data
// source's `tar.gz` type would.
func mustGzip(t *testing.T, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	_, err := gw.Write(contents)
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// mustZip builds a zip archive from a map of file path to content, as the
// `archive_file` data source's default type would.
func mustZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, path := range sortedPaths(files) {
		w, err := zw.Create(path)
		require.NoError(t, err)
		_, err = w.Write([]byte(files[path]))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// mustArchiveFile writes an archive to a temporary file and returns its path,
// for use as a version's `archive_file`.
func mustArchiveFile(t *testing.T, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, contents, 0o644))
	return path
}

// escapeTFInterpolation escapes Terraform's interpolation and directive
// markers, so that file contents embedded in a test configuration reach the
// provider verbatim instead of being evaluated by Terraform.
func escapeTFInterpolation(contents string) string {
	contents = strings.ReplaceAll(contents, "${", "$${")
	return strings.ReplaceAll(contents, "%{", "%%{")
}

// mustFilesMap converts a map of file path to content into the types.Map used
// by a version's `files` attribute.
func mustFilesMap(t *testing.T, files map[string]string) types.Map {
	t.Helper()
	m, diags := types.MapValueFrom(context.Background(), types.StringType, files)
	require.False(t, diags.HasError(), "building files map: %v", diags.Errors())
	return m
}

func TestComputeFilesHash(t *testing.T) {
	t.Parallel()

	base := computeFilesHash(map[string]string{
		"main.tf":          "resource \"null_resource\" \"a\" {}",
		"build/Dockerfile": "FROM ubuntu",
	})

	t.Run("StableAcrossMaps", func(t *testing.T) {
		t.Parallel()
		// A different map with the same entries must hash identically,
		// otherwise map iteration order would create a new version on
		// every plan.
		other := computeFilesHash(map[string]string{
			"build/Dockerfile": "FROM ubuntu",
			"main.tf":          "resource \"null_resource\" \"a\" {}",
		})
		require.Equal(t, base, other)
	})

	t.Run("ContentChanges", func(t *testing.T) {
		t.Parallel()
		require.NotEqual(t, base, computeFilesHash(map[string]string{
			"main.tf":          "resource \"null_resource\" \"a\" {}",
			"build/Dockerfile": "FROM debian",
		}))
	})

	t.Run("RenameChanges", func(t *testing.T) {
		t.Parallel()
		// Paths are hashed alongside contents, so moving a file is a change
		// even though the bytes are identical.
		require.NotEqual(t, base, computeFilesHash(map[string]string{
			"main.tf":             "resource \"null_resource\" \"a\" {}",
			"build/Containerfile": "FROM ubuntu",
		}))
	})

	t.Run("ConcatenationIsUnambiguous", func(t *testing.T) {
		t.Parallel()
		require.NotEqual(t,
			computeFilesHash(map[string]string{"a": "bc"}),
			computeFilesHash(map[string]string{"ab": "c"}),
		)
	})
}

func TestVersionContentHash(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"null_resource\" \"a\" {}"), 0o644))

	t.Run("Directory", func(t *testing.T) {
		t.Parallel()
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringValue(dir),
			Files:         types.MapNull(types.StringType),
			ArchiveBase64: types.StringNull(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		expected, err := computeDirectoryHash(dir)
		require.NoError(t, err)
		require.Equal(t, expected, hash.ValueString())
	})

	t.Run("Files", func(t *testing.T) {
		t.Parallel()
		files := map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"}
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         mustFilesMap(t, files),
			ArchiveBase64: types.StringNull(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.Equal(t, computeFilesHash(files), hash.ValueString())
	})

	t.Run("Archive", func(t *testing.T) {
		t.Parallel()
		archive := mustTarBase64(t, map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"})
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         types.MapNull(types.StringType),
			ArchiveBase64: types.StringValue(archive),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		decoded, err := base64.StdEncoding.DecodeString(archive)
		require.NoError(t, err)
		require.Equal(t, computeBytesHash(decoded), hash.ValueString())
	})

	t.Run("ArchiveFile", func(t *testing.T) {
		t.Parallel()
		contents := mustTar(t, map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"})
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Files:       types.MapNull(types.StringType),
			ArchiveFile: types.StringValue(mustArchiveFile(t, "template.tar", contents)),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.Equal(t, computeBytesHash(contents), hash.ValueString())
	})

	// A gzipped archive hashes as the tar it decompresses to, so recompressing
	// identical contents doesn't create a new version.
	t.Run("GzippedArchiveFile", func(t *testing.T) {
		t.Parallel()
		contents := mustTar(t, map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"})
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Files:       types.MapNull(types.StringType),
			ArchiveFile: types.StringValue(mustArchiveFile(t, "template.tar.gz", mustGzip(t, contents))),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.Equal(t, computeBytesHash(contents), hash.ValueString())
	})

	t.Run("UnknownArchiveFile", func(t *testing.T) {
		t.Parallel()
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Files:       types.MapNull(types.StringType),
			ArchiveFile: types.StringUnknown(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.True(t, hash.IsUnknown())
	})

	// Unlike an unknown path, a known path that isn't there is a mistake worth
	// failing the plan over, exactly like a missing `directory`.
	t.Run("MissingArchiveFile", func(t *testing.T) {
		t.Parallel()
		_, diags := versionContentHash(ctx, &TemplateVersion{
			Files:       types.MapNull(types.StringType),
			ArchiveFile: types.StringValue(filepath.Join(t.TempDir(), "absent.tar")),
		})
		require.True(t, diags.HasError())
	})

	// A source that isn't known yet must plan as a new version rather than
	// failing, since the contents can come from another resource.
	t.Run("UnknownFiles", func(t *testing.T) {
		t.Parallel()
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         types.MapUnknown(types.StringType),
			ArchiveBase64: types.StringNull(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.True(t, hash.IsUnknown())
	})

	t.Run("UnknownFileContent", func(t *testing.T) {
		t.Parallel()
		files, diags := types.MapValue(types.StringType, map[string]attr.Value{
			"main.tf": types.StringUnknown(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         files,
			ArchiveBase64: types.StringNull(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.True(t, hash.IsUnknown())
	})

	t.Run("UnknownDirectory", func(t *testing.T) {
		t.Parallel()
		hash, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringUnknown(),
			Files:         types.MapNull(types.StringType),
			ArchiveBase64: types.StringNull(),
		})
		require.False(t, diags.HasError(), "%v", diags.Errors())
		require.True(t, hash.IsUnknown())
	})

	t.Run("NoSource", func(t *testing.T) {
		t.Parallel()
		_, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         types.MapNull(types.StringType),
			ArchiveBase64: types.StringNull(),
		})
		require.True(t, diags.HasError())
	})

	// `stringvalidator.ExactlyOneOf` defers whenever one of them is unknown,
	// so this has to be caught here as well.
	t.Run("MultipleSources", func(t *testing.T) {
		t.Parallel()
		_, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringValue(dir),
			Files:         mustFilesMap(t, map[string]string{"main.tf": ""}),
			ArchiveBase64: types.StringNull(),
		})
		require.True(t, diags.HasError())
	})

	t.Run("NoTerraformFiles", func(t *testing.T) {
		t.Parallel()
		_, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         mustFilesMap(t, map[string]string{"nested/main.tf": ""}),
			ArchiveBase64: types.StringNull(),
		})
		require.True(t, diags.HasError())
	})

	t.Run("InvalidArchive", func(t *testing.T) {
		t.Parallel()
		_, diags := versionContentHash(ctx, &TemplateVersion{
			Directory:     types.StringNull(),
			Files:         types.MapNull(types.StringType),
			ArchiveBase64: types.StringValue("not base64!"),
		})
		require.True(t, diags.HasError())
	})
}

func TestDecodeArchive(t *testing.T) {
	t.Parallel()

	t.Run("Tar", func(t *testing.T) {
		t.Parallel()
		contentType, archive, err := decodeArchive(mustTarBase64(t, map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"}))
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeTar, contentType)
		require.NotEmpty(t, archive)
	})

	t.Run("NotBase64", func(t *testing.T) {
		t.Parallel()
		_, _, err := decodeArchive("not base64!")
		require.ErrorContains(t, err, "not valid base64")
	})

	t.Run("NotAnArchive", func(t *testing.T) {
		t.Parallel()
		_, _, err := decodeArchive(base64.StdEncoding.EncodeToString([]byte("resource \"null_resource\" \"a\" {}")))
		require.ErrorContains(t, err, "uncompressed tar, a gzipped tar, or a zip archive")
	})

	t.Run("TooBig", func(t *testing.T) {
		t.Parallel()
		_, _, err := decodeArchive(mustTarBase64(t, map[string]string{
			"main.tf": strings.Repeat("a", provisionersdk.TemplateArchiveLimit),
		}))
		require.ErrorContains(t, err, "too big")
	})
}

func TestNormalizeArchive(t *testing.T) {
	t.Parallel()

	files := map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"}

	t.Run("Tar", func(t *testing.T) {
		t.Parallel()
		tarred := mustTar(t, files)
		contentType, payload, err := normalizeArchive(tarred)
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeTar, contentType)
		require.Equal(t, tarred, payload)
	})

	// A gzipped tar is decompressed here, since the API only accepts tar and
	// zip.
	t.Run("GzippedTar", func(t *testing.T) {
		t.Parallel()
		tarred := mustTar(t, files)
		contentType, payload, err := normalizeArchive(mustGzip(t, tarred))
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeTar, contentType)
		require.Equal(t, tarred, payload)
	})

	// A zip is uploaded as-is: the API expands it server-side.
	t.Run("Zip", func(t *testing.T) {
		t.Parallel()
		zipped := mustZip(t, files)
		contentType, payload, err := normalizeArchive(zipped)
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeZip, contentType)
		require.Equal(t, zipped, payload)
	})

	t.Run("GzippedNonTar", func(t *testing.T) {
		t.Parallel()
		_, _, err := normalizeArchive(mustGzip(t, []byte("not a tar archive")))
		require.ErrorContains(t, err, "does not contain a tar archive")
	})

	t.Run("TruncatedZip", func(t *testing.T) {
		t.Parallel()
		zipped := mustZip(t, files)
		_, _, err := normalizeArchive(zipped[:len(zipped)/2])
		require.ErrorContains(t, err, "readable zip archive")
	})

	// A small archive must not be allowed to expand into an unbounded amount
	// of memory.
	t.Run("GzipBomb", func(t *testing.T) {
		t.Parallel()
		bomb := mustGzip(t, mustTar(t, map[string]string{
			"main.tf": strings.Repeat("a", provisionersdk.TemplateArchiveLimit),
		}))
		require.Less(t, len(bomb), provisionersdk.TemplateArchiveLimit)
		_, _, err := normalizeArchive(bomb)
		require.ErrorContains(t, err, "too big")
	})

	t.Run("ZipTooBig", func(t *testing.T) {
		t.Parallel()
		// Pseudo-random, and so incompressible, contents: the zip itself has
		// to exceed the limit for the size check to be the one that fails.
		big := make([]byte, provisionersdk.TemplateArchiveLimit)
		x := uint64(88172645463325252)
		for i := range big {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			big[i] = byte(x)
		}
		_, _, err := normalizeArchive(mustZip(t, map[string]string{"main.tf": string(big)}))
		require.ErrorContains(t, err, "too big")
	})
}

func TestReadArchiveFile(t *testing.T) {
	t.Parallel()

	files := map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"}

	t.Run("Tar", func(t *testing.T) {
		t.Parallel()
		tarred := mustTar(t, files)
		contentType, payload, err := readArchiveFile(mustArchiveFile(t, "template.tar", tarred))
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeTar, contentType)
		require.Equal(t, tarred, payload)
	})

	t.Run("Zip", func(t *testing.T) {
		t.Parallel()
		zipped := mustZip(t, files)
		contentType, payload, err := readArchiveFile(mustArchiveFile(t, "template.zip", zipped))
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeZip, contentType)
		require.Equal(t, zipped, payload)
	})

	// The extension is irrelevant: the format is detected from the contents,
	// so a mislabelled archive still works.
	t.Run("MislabelledExtension", func(t *testing.T) {
		t.Parallel()
		contentType, _, err := readArchiveFile(mustArchiveFile(t, "template.tar", mustZip(t, files)))
		require.NoError(t, err)
		require.Equal(t, codersdk.ContentTypeZip, contentType)
	})

	t.Run("Missing", func(t *testing.T) {
		t.Parallel()
		_, _, err := readArchiveFile(filepath.Join(t.TempDir(), "absent.tar"))
		require.ErrorContains(t, err, "failed to read `archive_file`")
	})

	t.Run("NotAnArchive", func(t *testing.T) {
		t.Parallel()
		_, _, err := readArchiveFile(mustArchiveFile(t, "template.tar", []byte("resource \"null_resource\" \"a\" {}")))
		require.ErrorContains(t, err, "uncompressed tar, a gzipped tar, or a zip archive")
	})
}

func TestWriteFiles(t *testing.T) {
	t.Parallel()

	t.Run("NestedPaths", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, writeFiles(dir, map[string]string{
			"main.tf":          "resource \"null_resource\" \"a\" {}",
			"build/Dockerfile": "FROM ubuntu",
		}))
		content, err := os.ReadFile(filepath.Join(dir, "build", "Dockerfile"))
		require.NoError(t, err)
		require.Equal(t, "FROM ubuntu", string(content))
	})

	t.Run("RejectsEscapingPaths", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{"../escape.tf", "/etc/passwd", "nested/../../escape.tf", ".."} {
			require.ErrorContains(t, writeFiles(t.TempDir(), map[string]string{path: ""}), "must not escape")
		}
	})
}

func TestAccTemplateResourceVersionSourceValidation(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	cfg := `
provider coderd {
	url   = "https://example.com"
	token = "abcd"
}

resource "coderd_template" "test" {
	name = "example-template"
	versions = [
		{
			active = true
			%s
		}
	]
}`

	// These all fail during validation, before the provider is configured, so
	// no Coder deployment is needed.
	const (
		directory     = "directory = \"./example-template\""
		files         = "files = { \"main.tf\" = \"\" }"
		archiveBase64 = "archive_base64 = \"\""
		archiveFile   = "archive_file = \"./example-template.tar\""
		sep           = "\n\t\t\t"
	)

	for name, source := range map[string]string{
		"NoSource":                ``,
		"DirectoryAndFiles":       directory + sep + files,
		"DirectoryAndArchive":     directory + sep + archiveBase64,
		"DirectoryAndArchiveFile": directory + sep + archiveFile,
		"FilesAndArchive":         files + sep + archiveBase64,
		"FilesAndArchiveFile":     files + sep + archiveFile,
		"BothArchives":            archiveBase64 + sep + archiveFile,
		"AllFour":                 directory + sep + files + sep + archiveBase64 + sep + archiveFile,
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				IsUnitTest:               true,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      fmt.Sprintf(cfg, source),
						ExpectError: regexp.MustCompile("Invalid Attribute Combination"),
					},
				},
			})
		})
	}
}

func TestAccTemplateResourceFiles(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	cfg := `
provider coderd {
	url   = %q
	token = %q
}

resource "coderd_template" "test" {
	name = "files-template"
	versions = [
		{
			name   = %q
			active = true
			files = {
				"main.tf"           = %q
				"terraform.tfvars"  = "name = \"world\""
			}
		}
	]
}`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_files_acc")

	mainTF, err := os.ReadFile("../../integration/template-test/example-template/main.tf")
	require.NoError(t, err)

	// The contents are interpolated into the test configuration as a string, so
	// the template's own interpolations have to be escaped to survive it.
	escapedMainTF := escapeTFInterpolation(string(mainTF))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// The contents are provided inline, and `terraform.tfvars` is read
			// as a variable value just like it is for a `directory`.
			{
				Config: fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), "one", escapedMainTF),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.id"),
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.directory_hash"),
					resource.TestCheckResourceAttr("coderd_template.test", "versions.0.name", "one"),
				),
			},
			// Changing the contents creates a new version.
			{
				Config: fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), "two", escapedMainTF+"\n# a change\n"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_template.test", "versions.0.name", "two"),
				),
			},
		},
	})
}

func TestAccTemplateResourceArchive(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	cfg := `
provider coderd {
	url   = %q
	token = %q
}

resource "coderd_template" "test" {
	name = "archive-template"
	versions = [
		{
			active         = true
			archive_base64 = %q
			tf_vars = [
				{
					name  = "name"
					value = "world"
				}
			]
		}
	]
}`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_archive_acc")

	mainTF, err := os.ReadFile("../../integration/template-test/example-template/main.tf")
	require.NoError(t, err)
	archive := mustTarBase64(t, map[string]string{"main.tf": string(mainTF)})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), archive),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.id"),
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.directory_hash"),
				),
			},
		},
	})
}

func TestAccTemplateResourceArchiveFile(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	cfg := `
provider coderd {
	url   = %q
	token = %q
}

resource "coderd_template" "test" {
	name = "archive-file-template"
	versions = [
		{
			active       = true
			archive_file = %q
			tf_vars = [
				{
					name  = "name"
					value = "world"
				}
			]
		}
	]
}`

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "template_resource_archive_file_acc")

	mainTF, err := os.ReadFile("../../integration/template-test/example-template/main.tf")
	require.NoError(t, err)
	files := map[string]string{"main.tf": string(mainTF)}
	// A zip, since that's what the `archive_file` data source produces by
	// default, and it exercises the server-side expansion.
	archive := mustArchiveFile(t, "template.zip", mustZip(t, files))

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(cfg, client.URL.String(), client.SessionToken(), archive),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.id"),
					resource.TestCheckResourceAttrSet("coderd_template.test", "versions.0.directory_hash"),
				),
			},
		},
	})
}

// TestAccTemplateResourceVersionSourcePlan checks that a version whose contents
// come from `files`, `archive_base64`, or `archive_file` plans without error,
// end to end. It only plans, so a mock server is enough. The hash the plan
// modifier produces is asserted in TestVersionsPlanModifierContentHash.
func TestAccTemplateResourceVersionSourcePlan(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	srv := newMockServer(nil)
	defer srv.Close()

	cfg := `
provider coderd {
	url   = %q
	token = "abcd"
}

resource "coderd_template" "test" {
	name = "example-template"
	versions = [
		{
			active = true
			%s
		}
	]
}`

	// The example template is used verbatim, since its interpolations are
	// exactly what a `files` entry has to carry through to the provider
	// untouched.
	mainTF, err := os.ReadFile("../../integration/template-test/example-template/main.tf")
	require.NoError(t, err)

	tarred := mustTar(t, map[string]string{"main.tf": string(mainTF)})

	for name, source := range map[string]string{
		"Files":       fmt.Sprintf("files = { \"main.tf\" = %q }", escapeTFInterpolation(string(mainTF))),
		"Archive":     fmt.Sprintf("archive_base64 = %q", base64.StdEncoding.EncodeToString(tarred)),
		"ArchiveFile": fmt.Sprintf("archive_file = %q", mustArchiveFile(t, "template.tar", tarred)),
		"ZippedArchiveFile": fmt.Sprintf("archive_file = %q",
			mustArchiveFile(t, "template.zip", mustZip(t, map[string]string{"main.tf": string(mainTF)}))),
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				IsUnitTest:               true,
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:   fmt.Sprintf(cfg, srv.URL, source),
						PlanOnly: true,
						// The resource doesn't exist yet, so the plan to
						// create it is non-empty by definition.
						ExpectNonEmptyPlan: true,
					},
				},
			})
		})
	}
}

// TestVersionsPlanModifierContentHash checks that the plan modifier hashes a
// version's contents regardless of which source attribute they come from,
// since an unknown or incorrect hash would create a new template version on
// every plan.
func TestVersionsPlanModifierContentHash(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	objType := versionObjectType()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"null_resource\" \"a\" {}"), 0o644))
	dirHash, err := computeDirectoryHash(dir)
	require.NoError(t, err)

	files := map[string]string{"main.tf": "resource \"null_resource\" \"a\" {}"}
	tarred := mustTar(t, files)
	archive := base64.StdEncoding.EncodeToString(tarred)
	zipped := mustZip(t, files)

	for name, tc := range map[string]struct {
		directory     attr.Value
		files         attr.Value
		archiveBase64 attr.Value
		archiveFile   attr.Value
		expected      string
	}{
		"Directory": {
			directory:     types.StringValue(dir),
			files:         types.MapNull(types.StringType),
			archiveBase64: types.StringNull(),
			archiveFile:   types.StringNull(),
			expected:      dirHash,
		},
		"Files": {
			directory:     types.StringNull(),
			files:         mustFilesMap(t, files),
			archiveBase64: types.StringNull(),
			archiveFile:   types.StringNull(),
			expected:      computeFilesHash(files),
		},
		"Archive": {
			directory:     types.StringNull(),
			files:         types.MapNull(types.StringType),
			archiveBase64: types.StringValue(archive),
			archiveFile:   types.StringNull(),
			expected:      computeBytesHash(tarred),
		},
		"ArchiveFile": {
			directory:     types.StringNull(),
			files:         types.MapNull(types.StringType),
			archiveBase64: types.StringNull(),
			archiveFile:   types.StringValue(mustArchiveFile(t, "template.tar", tarred)),
			expected:      computeBytesHash(tarred),
		},
		"ZippedArchiveFile": {
			directory:     types.StringNull(),
			files:         types.MapNull(types.StringType),
			archiveBase64: types.StringNull(),
			archiveFile:   types.StringValue(mustArchiveFile(t, "template.zip", zipped)),
			expected:      computeBytesHash(zipped),
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			versionVal, diags := types.ObjectValue(objType.AttrTypes, map[string]attr.Value{
				"id":               NewUUIDUnknown(),
				"name":             types.StringValue("main"),
				"message":          types.StringValue(""),
				"directory":        tc.directory,
				"files":            tc.files,
				"archive_base64":   tc.archiveBase64,
				"archive_file":     tc.archiveFile,
				"directory_hash":   types.StringUnknown(),
				"active":           types.BoolValue(true),
				"tf_vars":          types.SetNull(variableSetElemType),
				"provisioner_tags": types.SetNull(variableSetElemType),
			})
			require.False(t, diags.HasError(), "building version object: %v", diags.Errors())

			listVal, diags := types.ListValue(objType, []attr.Value{versionVal})
			require.False(t, diags.HasError(), "building list: %v", diags.Errors())

			resp := &planmodifier.ListResponse{PlanValue: listVal}
			NewVersionsPlanModifier().PlanModifyList(ctx, planmodifier.ListRequest{
				ConfigValue: listVal,
				PlanValue:   listVal,
			}, resp)
			require.False(t, resp.Diagnostics.HasError(), "PlanModifyList: %v", resp.Diagnostics.Errors())

			var planned []TemplateVersion
			diags = resp.PlanValue.ElementsAs(ctx, &planned, false)
			require.False(t, diags.HasError(), "decoding planned versions: %v", diags.Errors())
			require.Len(t, planned, 1)
			require.Equal(t, tc.expected, planned[0].DirectoryHash.ValueString())
			// There's no prior version with this hash, so a new one is created.
			require.True(t, planned[0].ID.IsUnknown())
		})
	}
}
