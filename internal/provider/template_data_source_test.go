package provider

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
)

func TestAccTemplateDataSource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "template_data_acc", true)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	orgID := firstUser.OrganizationIDs[0]

	version, err, _ := newVersion(ctx, client, newVersionRequest{
		OrganizationID: orgID,
		Version: &TemplateVersion{
			Name:      types.StringValue("main"),
			Message:   types.StringValue("Initial commit"),
			Directory: types.StringValue("../../integration/template-test/example-template/"),
			TerraformVariables: []Variable{
				{
					Name:  types.StringValue("name"),
					Value: types.StringValue("world"),
				},
			},
		},
	})
	require.NoError(t, err)
	tpl, err := client.CreateTemplate(ctx, orgID, codersdk.CreateTemplateRequest{
		Name:               "example-template",
		DisplayName:        "Example Template",
		Description:        "An example template",
		Icon:               "/path/to/icon.png",
		VersionID:          version.ID,
		DefaultTTLMillis:   ptr.Ref((10 * time.Hour).Milliseconds()),
		ActivityBumpMillis: ptr.Ref((4 * time.Hour).Milliseconds()),
		AutostopRequirement: &codersdk.TemplateAutostopRequirement{
			DaysOfWeek: []string{"sunday"},
			Weeks:      1,
		},
		AutostartRequirement: &codersdk.TemplateAutostartRequirement{
			DaysOfWeek: []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"},
		},
		AllowUserCancelWorkspaceJobs:   ptr.Ref(true),
		AllowUserAutostart:             ptr.Ref(true),
		AllowUserAutostop:              ptr.Ref(true),
		FailureTTLMillis:               ptr.Ref((1 * time.Hour).Milliseconds()),
		TimeTilDormantMillis:           ptr.Ref((7 * 24 * time.Hour).Milliseconds()),
		TimeTilDormantAutoDeleteMillis: ptr.Ref((30 * 24 * time.Hour).Milliseconds()),
		DisableEveryoneGroupAccess:     true,
		RequireActiveVersion:           true,
	})
	require.NoError(t, err)

	// Can't set some fields on create, like deprecated.
	tpl, err = client.UpdateTemplateMeta(ctx, tpl.ID, codersdk.UpdateTemplateMeta{
		Name:               tpl.Name,
		DisplayName:        tpl.DisplayName,
		Description:        tpl.Description,
		Icon:               tpl.Icon,
		DefaultTTLMillis:   tpl.DefaultTTLMillis,
		ActivityBumpMillis: tpl.ActivityBumpMillis,
		AutostopRequirement: &codersdk.TemplateAutostopRequirement{
			DaysOfWeek: tpl.AutostopRequirement.DaysOfWeek,
			Weeks:      tpl.AutostopRequirement.Weeks,
		},
		AutostartRequirement: &codersdk.TemplateAutostartRequirement{
			DaysOfWeek: tpl.AutostartRequirement.DaysOfWeek,
		},
		AllowUserAutostart:             tpl.AllowUserAutostart,
		AllowUserAutostop:              tpl.AllowUserAutostop,
		AllowUserCancelWorkspaceJobs:   tpl.AllowUserCancelWorkspaceJobs,
		FailureTTLMillis:               tpl.FailureTTLMillis,
		TimeTilDormantMillis:           tpl.TimeTilDormantMillis,
		TimeTilDormantAutoDeleteMillis: tpl.TimeTilDormantAutoDeleteMillis,
		UpdateWorkspaceLastUsedAt:      false,
		UpdateWorkspaceDormantAt:       false,
		RequireActiveVersion:           tpl.RequireActiveVersion,
		DeprecationMessage:             ptr.Ref("This template is deprecated"),
		DisableEveryoneGroupAccess:     true,
		MaxPortShareLevel:              ptr.Ref(codersdk.WorkspaceAgentPortShareLevelOwner),
	})
	require.NoError(t, err)

	err = client.UpdateTemplateACL(ctx, tpl.ID, codersdk.UpdateTemplateACL{
		UserPerms: map[string]codersdk.TemplateRole{
			firstUser.ID.String(): codersdk.TemplateRoleAdmin,
		},
		GroupPerms: map[string]codersdk.TemplateRole{
			firstUser.OrganizationIDs[0].String(): codersdk.TemplateRoleUse,
		},
	})
	require.NoError(t, err)

	checkFn := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.coderd_template.test", "organization_id", tpl.OrganizationID.String()),
		resource.TestCheckResourceAttr("data.coderd_template.test", "id", tpl.ID.String()),
		resource.TestCheckResourceAttr("data.coderd_template.test", "name", tpl.Name),
		resource.TestCheckResourceAttr("data.coderd_template.test", "display_name", tpl.DisplayName),
		resource.TestCheckResourceAttr("data.coderd_template.test", "description", tpl.Description),
		resource.TestCheckResourceAttr("data.coderd_template.test", "active_version_id", tpl.ActiveVersionID.String()),
		resource.TestCheckResourceAttr("data.coderd_template.test", "active_user_count", strconv.Itoa(tpl.ActiveUserCount)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "deprecated", strconv.FormatBool(tpl.Deprecated)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "deprecation_message", tpl.DeprecationMessage),
		resource.TestCheckResourceAttr("data.coderd_template.test", "icon", tpl.Icon),
		resource.TestCheckResourceAttr("data.coderd_template.test", "default_ttl_ms", strconv.FormatInt(tpl.DefaultTTLMillis, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "activity_bump_ms", strconv.FormatInt(tpl.ActivityBumpMillis, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "auto_stop_requirement.days_of_week.#", strconv.FormatInt(int64(len(tpl.AutostopRequirement.DaysOfWeek)), 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "auto_stop_requirement.weeks", strconv.FormatInt(tpl.AutostopRequirement.Weeks, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "auto_start_permitted_days_of_week.#", strconv.FormatInt(int64(len(tpl.AutostartRequirement.DaysOfWeek)), 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "allow_user_cancel_workspace_jobs", "true"),
		resource.TestCheckResourceAttr("data.coderd_template.test", "allow_user_autostart", strconv.FormatBool(tpl.AllowUserAutostart)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "allow_user_autostop", strconv.FormatBool(tpl.AllowUserAutostop)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "allow_user_cancel_workspace_jobs", strconv.FormatBool(tpl.AllowUserCancelWorkspaceJobs)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "failure_ttl_ms", strconv.FormatInt(tpl.FailureTTLMillis, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "time_til_dormant_ms", strconv.FormatInt(tpl.TimeTilDormantMillis, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "time_til_dormant_autodelete_ms", strconv.FormatInt(tpl.TimeTilDormantAutoDeleteMillis, 10)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "require_active_version", strconv.FormatBool(tpl.RequireActiveVersion)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "max_port_share_level", string(tpl.MaxPortShareLevel)),
		resource.TestCheckResourceAttr("data.coderd_template.test", "created_by_user_id", firstUser.ID.String()),
		resource.TestCheckResourceAttr("data.coderd_template.test", "created_at", strconv.Itoa(int(tpl.CreatedAt.Unix()))),
		resource.TestCheckResourceAttr("data.coderd_template.test", "updated_at", strconv.Itoa(int(tpl.UpdatedAt.Unix()))),
		resource.TestCheckResourceAttr("data.coderd_template.test", "acl.groups.#", "1"),
		resource.TestCheckResourceAttr("data.coderd_template.test", "acl.users.#", "1"),
		resource.TestMatchTypeSetElemNestedAttrs("data.coderd_template.test", "acl.groups.*", map[string]*regexp.Regexp{
			"id":   regexp.MustCompile(firstUser.OrganizationIDs[0].String()),
			"role": regexp.MustCompile("^use$"),
		}),
		resource.TestMatchTypeSetElemNestedAttrs("data.coderd_template.test", "acl.users.*", map[string]*regexp.Regexp{
			"id":   regexp.MustCompile(firstUser.ID.String()),
			"role": regexp.MustCompile("^admin$"),
		}),
	)

	t.Run("TemplateByOrgAndNameOK", func(t *testing.T) {
		cfg := testAccTemplateDataSourceConfig{
			URL:            client.URL.String(),
			Token:          client.SessionToken(),
			OrganizationID: ptr.Ref(orgID.String()),
			Name:           ptr.Ref(tpl.Name),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
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

	t.Run("TemplateByIDOK", func(t *testing.T) {
		cfg := testAccTemplateDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    ptr.Ref(tpl.ID.String()),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
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

	t.Run("NeitherIDNorNameError", func(t *testing.T) {
		cfg := testAccTemplateDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`At least one attribute out of \[name,id\] must be specified`),
				},
			},
		})
	})

	t.Run("NameWithoutOrgUsesDefaultOrg", func(t *testing.T) {
		cfg := testAccTemplateDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Name:  ptr.Ref(tpl.Name),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
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
}

type testAccTemplateDataSourceConfig struct {
	URL   string
	Token string

	OrganizationID *string
	ID             *string
	Name           *string
}

func (c testAccTemplateDataSourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url   = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_template" "test" {
	organization_id = {{orNull .OrganizationID}}
	id              = {{orNull .ID}}
	name            = {{orNull .Name}}
}`

	funcMap := template.FuncMap{
		"orNull": printOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("templateDataSource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
