package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/stretchr/testify/require"
)

func testMCPServerTerraformVersionChecks() []tfversion.TerraformVersionCheck {
	return []tfversion.TerraformVersionCheck{
		tfversion.SkipBelow(tfversion.Version1_11_0),
	}
}

func TestAccMCPServerResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "mcp_server_acc")
	organizations, err := client.Organizations(ctx)
	require.NoError(t, err, "list organizations")
	require.NotEmpty(t, organizations, "first user must belong to an organization")

	// Main devel builds report the previous minor's version, so a semver minimum
	// cannot distinguish them from releases that do not have this route.
	_, err = client.MCPServerConfigs(ctx, organizations[0].ID)
	if err != nil {
		var sdkErr *codersdk.Error
		if errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound {
			t.Skipf("deployment does not support org-scoped MCP server configs")
		}
		require.NoError(t, err, "probe org-scoped MCP server configs")
	}

	minimal := testAccMCPServerResourceConfig{
		URL:         client.URL.String(),
		Token:       client.SessionToken(),
		DisplayName: "MCP Acceptance",
		Slug:        "mcp-acceptance",
		ServerURL:   "https://mcp.example.com/v1",
	}
	updated := minimal
	updated.DisplayName = "MCP Acceptance Updated"
	updated.Description = "Managed by Terraform acceptance tests"
	updated.ServerURL = "https://mcp.example.com/v2"
	updated.Enabled = true
	updated.Availability = "default_on"
	updated.ToolAllowList = []string{"search", "read"}
	updated.ToolDenyList = []string{"delete"}

	renamed := updated
	renamed.Slug = "mcp-acceptance-renamed"

	apiKey := renamed
	apiKey.AuthType = "api_key"
	apiKey.APIKeyHeader = "Authorization"
	apiKey.APIKeyValue = "secret-one"
	apiKey.APIKeyValueVersion = 1

	rotated := apiKey
	rotated.APIKeyValue = "secret-two"
	rotated.APIKeyValueVersion = 2

	noAuth := renamed
	noAuth.APIKeyValueVersion = 2

	// Mirrors adopting an imported server: the write-only secret cannot be
	// read back, so a config omitting it must plan and apply cleanly while the
	// server keeps the stored key.
	adopted := rotated
	adopted.APIKeyValue = ""
	adopted.APIKeyValueVersion = 0

	createMissingSecret := minimal
	createMissingSecret.AuthType = "api_key"
	createMissingSecret.APIKeyHeader = "Authorization"

	createEmptySecret := createMissingSecret
	createEmptySecret.EmptyAPIKeyValue = true
	createEmptySecret.APIKeyValueVersion = 1

	rotationMissingSecret := rotated
	rotationMissingSecret.APIKeyValue = ""
	rotationMissingSecret.APIKeyValueVersion = 3

	transitionMissingHeader := noAuth
	transitionMissingHeader.AuthType = "api_key"
	transitionMissingHeader.APIKeyValue = "secret-three"
	transitionMissingHeader.APIKeyValueVersion = 3

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   testMCPServerTerraformVersionChecks(),
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      createMissingSecret.String(t),
				ExpectError: regexp.MustCompile("Missing API Key Value"),
			},
			{
				Config:      createEmptySecret.String(t),
				ExpectError: regexp.MustCompile("Invalid Attribute Value"),
			},
			{
				Config: minimal.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "display_name", minimal.DisplayName),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "slug", minimal.Slug),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "description", ""),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "icon_url", ""),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "transport", "streamable_http"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "auth_type", "none"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "availability", "default_off"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "enabled", "false"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "model_intent", "false"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "allow_in_plan_mode", "false"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "forward_coder_headers", "false"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "tool_allow_list.#", "0"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "tool_deny_list.#", "0"),
					resource.TestCheckResourceAttrSet("coderd_mcp_server.test", "organization_id"),
					resource.TestCheckResourceAttrSet("coderd_mcp_server.test", "created_at"),
					resource.TestCheckResourceAttrSet("coderd_mcp_server.test", "updated_at"),
				),
			},
			{
				Config: updated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "display_name", updated.DisplayName),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "description", updated.Description),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "url", updated.ServerURL),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "enabled", "true"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "availability", updated.Availability),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "tool_allow_list.#", "2"),
					resource.TestCheckTypeSetElemAttr("coderd_mcp_server.test", "tool_allow_list.*", "search"),
					resource.TestCheckTypeSetElemAttr("coderd_mcp_server.test", "tool_allow_list.*", "read"),
					resource.TestCheckTypeSetElemAttr("coderd_mcp_server.test", "tool_deny_list.*", "delete"),
				),
			},
			{
				Config: renamed.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_mcp_server.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("coderd_mcp_server.test", "slug", renamed.Slug),
			},
			{
				Config: apiKey.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_header", apiKey.APIKeyHeader),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_value_wo_version", "1"),
					resource.TestCheckNoResourceAttr("coderd_mcp_server.test", "api_key_value_wo"),
					checkMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config: rotated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_value_wo_version", "2"),
					checkMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config:      rotationMissingSecret.String(t),
				ExpectError: regexp.MustCompile("Missing API Key Value"),
			},
			{
				Config: noAuth.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "auth_type", "none"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_header", ""),
					checkMCPServerAPIKey(ctx, t, client, false),
				),
			},
			{
				Config:      transitionMissingHeader.String(t),
				ExpectError: regexp.MustCompile("Missing API Key Header"),
			},
			{
				Config: rotated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_header", rotated.APIKeyHeader),
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "api_key_value_wo_version", "2"),
					checkMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config: adopted.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckNoResourceAttr("coderd_mcp_server.test", "api_key_value_wo_version"),
					checkMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				ResourceName:      "coderd_mcp_server.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["coderd_mcp_server.test"]
					if !ok {
						return "", fmt.Errorf("coderd_mcp_server.test not found in state")
					}
					return rs.Primary.Attributes["organization_id"] + "/" + rs.Primary.ID, nil
				},
				ImportStateVerifyIgnore: []string{
					"oauth2_client_secret_wo",
					"oauth2_client_secret_wo_version",
					"api_key_value_wo",
					"api_key_value_wo_version",
					"custom_headers_wo",
					"custom_headers_wo_version",
				},
			},
		},
	})
}

type testAccMCPServerResourceConfig struct {
	URL                string
	Token              string
	DisplayName        string
	Slug               string
	ServerURL          string
	Description        string
	Enabled            bool
	Availability       string
	ToolAllowList      []string
	ToolDenyList       []string
	AuthType           string
	APIKeyHeader       string
	APIKeyValue        string
	EmptyAPIKeyValue   bool
	APIKeyValueVersion int
}

func (c testAccMCPServerResourceConfig) String(t *testing.T) string {
	t.Helper()
	const tpl = `
provider "coderd" {
  url   = "{{.URL}}"
  token = "{{.Token}}"
}

resource "coderd_mcp_server" "test" {
  display_name = "{{.DisplayName}}"
  slug         = "{{.Slug}}"
  url          = "{{.ServerURL}}"
{{- if .Description }}
  description = "{{.Description}}"
{{- end }}
{{- if .Enabled }}
  enabled = true
{{- end }}
{{- if .Availability }}
  availability = "{{.Availability}}"
{{- end }}
{{- if .ToolAllowList }}
  tool_allow_list = [{{range $i, $v := .ToolAllowList}}{{if $i}}, {{end}}"{{$v}}"{{end}}]
{{- end }}
{{- if .ToolDenyList }}
  tool_deny_list = [{{range $i, $v := .ToolDenyList}}{{if $i}}, {{end}}"{{$v}}"{{end}}]
{{- end }}
{{- if .AuthType }}
  auth_type = "{{.AuthType}}"
{{- end }}
{{- if .APIKeyHeader }}
  api_key_header = "{{.APIKeyHeader}}"
{{- end }}
{{- if .APIKeyValue }}
  api_key_value_wo = "{{.APIKeyValue}}"
{{- end }}
{{- if .EmptyAPIKeyValue }}
  api_key_value_wo = ""
{{- end }}
{{- if .APIKeyValueVersion }}
  api_key_value_wo_version = {{.APIKeyValueVersion}}
{{- end }}
}
`
	var out bytes.Buffer
	require.NoError(t, template.Must(template.New("mcpServerResource").Parse(tpl)).Execute(&out, c))
	return out.String()
}

func checkMCPServerAPIKey(ctx context.Context, t *testing.T, client *codersdk.Client, want bool) resource.TestCheckFunc {
	t.Helper()
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["coderd_mcp_server.test"]
		if !ok {
			return fmt.Errorf("coderd_mcp_server.test not found in state")
		}
		organizationID, err := uuid.Parse(rs.Primary.Attributes["organization_id"])
		if err != nil {
			return err
		}
		id, err := uuid.Parse(rs.Primary.ID)
		if err != nil {
			return err
		}
		server, err := client.MCPServerConfigByID(ctx, organizationID, id)
		if err != nil {
			return err
		}
		if server.HasAPIKey != want {
			return fmt.Errorf("MCP server %s HasAPIKey = %t, want %t", server.ID, server.HasAPIKey, want)
		}
		return nil
	}
}
