package provider

import (
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestAccOrganizationSyncSettingsResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "organization_sync_settings_acc", true)
	_, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)

	cfg1 := testAccOrganizationSyncSettingsResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),

		Field:         "wibble",
		AssignDefault: true,
	}

	cfg2 := cfg1
	cfg2.Field = "wobble"
	cfg2.AssignDefault = false

	cfg3 := cfg2
	cfg3.Mapping = map[string][]uuid.UUID{
		"wibble": {uuid.MustParse("151b5a4e-391a-464d-a88c-ac50f1458d6f")},
	}

	t.Run("CreateUpdateReadOk", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				// Create and Read
				{
					Config: cfg1.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("field"), knownvalue.StringExact("wibble")),
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("assign_default"), knownvalue.Bool(true)),
					},
				},
				// Update and Read
				{
					Config: cfg2.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("field"), knownvalue.StringExact("wobble")),
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("assign_default"), knownvalue.Bool(false)),
					},
				},
				// Add mapping
				{
					Config: cfg3.String(t),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("field"), knownvalue.StringExact("wobble")),
						statecheck.ExpectKnownValue("coderd_organization_sync_settings.test", tfjsonpath.New("mapping").AtMapKey("wibble").AtSliceIndex(0), knownvalue.StringExact("151b5a4e-391a-464d-a88c-ac50f1458d6f")),
					},
				},
			},
		})
	})
}

type testAccOrganizationSyncSettingsResourceConfig struct {
	URL   string
	Token string

	Field         string
	AssignDefault bool
	Mapping       map[string][]uuid.UUID
}

func (c testAccOrganizationSyncSettingsResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_organization_sync_settings" "test" {
	field          = "{{.Field}}"
	assign_default = {{.AssignDefault}}

	{{- if .Mapping}}
	mapping = {
		{{- range $key, $value := .Mapping}}
		{{$key}} = [
			{{- range $id := $value}}
			"{{$id}}",
			{{- end}}
		]
		{{- end}}
	}
	{{- end}}
}
`
	funcMap := template.FuncMap{}

	buf := strings.Builder{}
	tmpl, err := template.New("organizationSyncSettingsResource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
