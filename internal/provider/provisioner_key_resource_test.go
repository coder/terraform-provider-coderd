package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccProvisionerKeyResource(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "license_acc", true)
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
	cfg2.Name = "different-provisioner-key"
	cfg2.Tags = map[string]string{
		"wibble": "wobble",
	}

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
