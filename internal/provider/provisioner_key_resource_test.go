package provider

import (
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestAccProvisionerKeyResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "provisioner_key_acc", true)
	orgs, err := client.Organizations(ctx)
	require.NoError(t, err)
	firstOrg := orgs[0].ID

	cfg1 := testAccProvisionerKeyResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),

		OrganizationID: firstOrg,
		Name:           "example-provisioner-key",
	}

	cfg2 := cfg1
	cfg2.Tags = map[string]string{
		"wibble": "wobble",
	}

	cfg3 := cfg2
	cfg3.Name = "different-provisioner-key"

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
			},
			{
				Config: cfg2.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_provisioner_key.test", plancheck.ResourceActionReplace),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("coderd_provisioner_key.test", tfjsonpath.New("tags").AtMapKey("wibble"), knownvalue.StringExact("wobble")),
				},
			},
			{
				Config: cfg3.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_provisioner_key.test", plancheck.ResourceActionReplace),
					},
				},
			},
		},
	})
}

type testAccProvisionerKeyResourceConfig struct {
	URL   string
	Token string

	OrganizationID uuid.UUID
	Name           string
	Tags           map[string]string
}

func (c testAccProvisionerKeyResourceConfig) String(t *testing.T) string {
	t.Helper()

	tpl := `
provider coderd {
	url   = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_provisioner_key" "test" {
	organization_id = "{{.OrganizationID}}"
	name            = "{{.Name}}"

	tags = {
		{{- range $key, $value := .Tags}}
		{{$key}} = "{{$value}}"
		{{- end}}
	}
}
`

	buf := strings.Builder{}
	tmpl, err := template.New("provisionerKeyResource").Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
