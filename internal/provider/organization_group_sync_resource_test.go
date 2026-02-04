package provider

import (
	"os"
	"regexp"
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

func TestAccOrganizationGroupSyncResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "organization_group_sync_acc", integration.UseLicense)
	_, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	// Create an organization first
	org, err := client.CreateOrganization(ctx, codersdk.CreateOrganizationRequest{
		Name:        "test-org",
		DisplayName: "Test Organization",
	})
	require.NoError(t, err)

	cfg1 := testAccOrganizationGroupSyncResourceConfig{
		URL:            client.URL.String(),
		Token:          client.SessionToken(),
		OrganizationID: org.ID.String(),
		Field:          "groups",
		Mapping:        map[string][]string{}, // Empty mapping
	}

	cfg2 := cfg1
	cfg2.Field = "updated_groups"
	cfg2.RegexFilter = ptr.Ref(".*test.*")
	cfg2.AutoCreateMissing = ptr.Ref(true)
	cfg2.Mapping = map[string][]string{
		"test_group": {"6e57187f-6543-46ab-a62c-a10065dd4314"},
	}

	cfg3 := cfg2
	cfg3.Mapping = map[string][]string{
		"new_group": {"6e57187f-6543-46ab-a62c-a10065dd4314"},
	}

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
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("organization_id"), knownvalue.StringExact(org.ID.String())),
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("field"), knownvalue.StringExact("groups")),
					},
				},
				// Import
				{
					Config:                               cfg1.String(t),
					ResourceName:                         "coderd_organization_group_sync.test",
					ImportState:                          true,
					ImportStateVerify:                    true,
					ImportStateId:                        org.ID.String(),
					ImportStateVerifyIdentifierAttribute: "organization_id",
				},
				// Update and Read
				{
					Config: cfg2.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("field"), knownvalue.StringExact("updated_groups")),
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("regex_filter"), knownvalue.StringExact(".*test.*")),
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("auto_create_missing"), knownvalue.Bool(true)),
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("mapping").AtMapKey("test_group").AtSliceIndex(0), knownvalue.StringExact("6e57187f-6543-46ab-a62c-a10065dd4314")),
					},
				},
				// Update mapping
				{
					Config: cfg3.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("mapping").AtMapKey("new_group").AtSliceIndex(0), knownvalue.StringExact("6e57187f-6543-46ab-a62c-a10065dd4314")),
					},
				},
			},
		})
	})

	t.Run("MinimalConfig", func(t *testing.T) {
		minimalCfg := testAccOrganizationGroupSyncResourceConfig{
			URL:            client.URL.String(),
			Token:          client.SessionToken(),
			OrganizationID: org.ID.String(),
			Field:          "minimal",
			Mapping:        map[string][]string{}, // Empty mapping
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: minimalCfg.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("field"), knownvalue.StringExact("minimal")),
						statecheck.ExpectKnownValue("coderd_organization_group_sync.test", tfjsonpath.New("organization_id"), knownvalue.StringExact(org.ID.String())),
					},
				},
			},
		})
	})

	t.Run("InvalidRegexFilter", func(t *testing.T) {
		invalidRegexCfg := testAccOrganizationGroupSyncResourceConfig{
			URL:            client.URL.String(),
			Token:          client.SessionToken(),
			OrganizationID: org.ID.String(),
			Field:          "invalid_regex",
			RegexFilter:    ptr.Ref("[invalid"),
			Mapping:        map[string][]string{},
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      invalidRegexCfg.String(t),
					ExpectError: regexp.MustCompile("error parsing regexp"),
				},
			},
		})
	})
}

type testAccOrganizationGroupSyncResourceConfig struct {
	URL            string
	Token          string
	OrganizationID string

	Field             string
	RegexFilter       *string
	AutoCreateMissing *bool
	Mapping           map[string][]string
}

func (c testAccOrganizationGroupSyncResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_organization_group_sync" "test" {
	organization_id = "{{.OrganizationID}}"
	field = {{printf "%q" .Field}}
	{{- if .RegexFilter}}
	regex_filter = {{orNull .RegexFilter}}
	{{- end}}
	{{- if .AutoCreateMissing}}
	auto_create_missing = {{.AutoCreateMissing}}
	{{- end}}
	mapping = {
		{{- range $key, $value := .Mapping}}
		{{$key}} = {{printf "%q" $value}}
		{{- end}}
	}
}
`
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("organizationGroupSyncResource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
