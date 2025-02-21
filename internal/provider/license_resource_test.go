package provider

import (
	"context"
	"os"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/require"
)

func TestAccLicenseResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := context.Background()
	client := integration.StartCoder(ctx, t, "license_acc", false)

	license := os.Getenv("CODER_ENTERPRISE_LICENSE")
	if license == "" {
		t.Skip("No license found for license resource tests, skipping")
	}

	cfg1 := testAccLicenseResourceConfig{
		URL:     client.URL.String(),
		Token:   client.SessionToken(),
		License: license,
	}

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg1.String(t),
			},
		},
	})
}

type testAccLicenseResourceConfig struct {
	URL     string
	Token   string
	License string
}

func (c testAccLicenseResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url   = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_license" "test" {
	license = "{{.License}}"
}
`
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("licenseResource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
