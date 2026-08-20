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
	"time"

	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
	"github.com/stretchr/testify/require"
)

func testAgentsMCPServerTerraformVersionChecks() []tfversion.TerraformVersionCheck {
	return []tfversion.TerraformVersionCheck{
		tfversion.SkipBelow(tfversion.Version1_11_0),
	}
}

func TestAccAgentsMCPServerResourceSchemaValidation(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for name, tc := range map[string]struct {
		body      string
		extra     string
		wantError string
	}{
		"oauth2 unknown auth url with missing token url": {
			// The unknown endpoint cannot make this valid: the configured
			// client ID rules out all-unset and the omitted token URL rules
			// out all-set, so the plan must fail without waiting for apply.
			body: `  auth_type        = "oauth2"
  oauth2_client_id = "client-id"
  oauth2_auth_url  = terraform_data.endpoint.output`,
			extra: `
resource "terraform_data" "endpoint" {
  input = "https://issuer.example.com/authorize"
}
`,
			wantError: "Incomplete OAuth2 Configuration",
		},
		"api key missing header": {
			body: `  auth_type               = "api_key"
  api_key_value_wo        = "secret"
  api_key_value_wo_version = 1`,
			wantError: "Missing API Key Header",
		},
		"api key missing value": {
			body: `  auth_type     = "api_key"
  api_key_header = "Authorization"`,
			wantError: "Missing API Key Value",
		},
		"api key empty value": {
			body: `  auth_type               = "api_key"
  api_key_header          = "Authorization"
  api_key_value_wo        = ""
  api_key_value_wo_version = 1`,
			wantError: "Invalid Attribute Value",
		},
		"api key empty header": {
			body: `  auth_type               = "api_key"
  api_key_header          = ""
  api_key_value_wo        = "secret"
  api_key_value_wo_version = 1`,
			wantError: "Invalid Attribute Value",
		},
		"custom headers missing map": {
			body:      `  auth_type = "custom_headers"`,
			wantError: "Missing Custom Headers",
		},
		"api key header with default none auth": {
			body:      `  api_key_header = "Authorization"`,
			wantError: "Invalid Attribute Combination",
		},
		"oauth2 client id with api_key auth": {
			body: `  auth_type                = "api_key"
  api_key_header           = "Authorization"
  api_key_value_wo         = "secret"
  api_key_value_wo_version = 1
  oauth2_client_id         = "client-id"`,
			wantError: "Invalid Attribute Combination",
		},
		"oauth2 partial manual config": {
			body: `  auth_type        = "oauth2"
  oauth2_client_id = "client-id"`,
			wantError: "Incomplete OAuth2 Configuration",
		},
		"oauth2 empty client id": {
			body: `  auth_type        = "oauth2"
  oauth2_client_id = ""
  oauth2_auth_url  = "https://issuer.example.com/authorize"
  oauth2_token_url = "https://issuer.example.com/token"`,
			wantError: "Invalid Attribute Value",
		},
		"oauth2 empty auth url": {
			body: `  auth_type        = "oauth2"
  oauth2_client_id = "client-id"
  oauth2_auth_url  = ""
  oauth2_token_url = "https://issuer.example.com/token"`,
			wantError: "Invalid Attribute Value",
		},
		"oauth2 empty token url": {
			body: `  auth_type        = "oauth2"
  oauth2_client_id = "client-id"
  oauth2_auth_url  = "https://issuer.example.com/authorize"
  oauth2_token_url = ""`,
			wantError: "Invalid Attribute Value",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newMockServer(nil)
			defer srv.Close()

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				TerraformVersionChecks:   testAgentsMCPServerTerraformVersionChecks(),
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      testAgentsMCPServerValidationConfig(srv.URL, tc.body) + tc.extra,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(tc.wantError),
				}},
			})
		})
	}
}

func testAgentsMCPServerValidationConfig(url, body string) string {
	return `provider "coderd" {
  url   = "` + url + `"
  token = "test-token"
}

resource "coderd_agents_mcp_server" "test" {
  display_name = "MCP Test"
  slug         = "mcp-test"
  url          = "https://mcp.example.com/v1"
` + body + `
}
`
}

// TestAccAgentsMCPServerRequiredStringsRejectEmpty guards that the required strings
// reject a configured "", which Terraform otherwise treats as present.
func TestAccAgentsMCPServerRequiredStringsRejectEmpty(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	for _, attr := range []string{"display_name", "slug", "url"} {
		t.Run(attr, func(t *testing.T) {
			t.Parallel()

			srv := newMockServer(nil)
			defer srv.Close()

			values := map[string]string{
				"display_name": `"MCP Test"`,
				"slug":         `"mcp-test"`,
				"url":          `"https://mcp.example.com/v1"`,
			}
			values[attr] = `""`
			config := `provider "coderd" {
  url   = "` + srv.URL + `"
  token = "test-token"
}

resource "coderd_agents_mcp_server" "test" {
  display_name = ` + values["display_name"] + `
  slug         = ` + values["slug"] + `
  url          = ` + values["url"] + `
}
`
			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				TerraformVersionChecks:   testAgentsMCPServerTerraformVersionChecks(),
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      config,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile("Invalid Attribute Value"),
				}},
			})
		})
	}
}

func TestAgentsMCPServerUpdateRequestSecrets(t *testing.T) {
	t.Parallel()

	t.Run("rotation attaches secret", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("api_key")
		state.APIKeyHeader = types.StringValue("Authorization")
		state.APIKeyValueWOVersion = types.Int64Value(1)
		plan := state
		plan.APIKeyValueWOVersion = types.Int64Value(2)
		config := plan
		config.APIKeyValueWO = types.StringValue("rotated-secret")

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, config, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, request.APIKeyValue)
		require.Equal(t, "rotated-secret", *request.APIKeyValue)
	})

	t.Run("rotation without secret is rejected", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("api_key")
		state.APIKeyHeader = types.StringValue("Authorization")
		state.APIKeyValueWOVersion = types.Int64Value(1)
		plan := state
		plan.APIKeyValueWOVersion = types.Int64Value(2)

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, plan, &diags)
		require.True(t, diags.HasError())
		require.Contains(t, diags.Errors()[0].Summary(), "Missing API Key Value")
		require.Nil(t, request.APIKeyValue)
	})

	t.Run("non-destination version bump is ignored", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("oauth2")
		state.APIKeyValueWOVersion = types.Int64Value(1)
		plan := state
		plan.APIKeyValueWOVersion = types.Int64Value(2)
		config := plan
		config.APIKeyValueWO = types.StringValue("stale-api-key")

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, config, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.Nil(t, request.APIKeyValue)
	})

	t.Run("transition attaches unchanged-version secret", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("none")
		state.APIKeyValueWOVersion = types.Int64Value(2)
		plan := state
		plan.AuthType = types.StringValue("api_key")
		plan.APIKeyHeader = types.StringValue("Authorization")
		config := plan
		config.APIKeyValueWO = types.StringValue("restored-secret")

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, config, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, request.APIKeyValue)
		require.Equal(t, "restored-secret", *request.APIKeyValue)
	})

	t.Run("adoption preserves unmanaged secret", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("api_key")
		state.APIKeyHeader = types.StringValue("Authorization")
		plan := state

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, plan, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.Nil(t, request.APIKeyValue)
	})
}

func TestAgentsMCPServerUpdateRequestSparse(t *testing.T) {
	t.Parallel()

	t.Run("only changed fields are sent", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("api_key")
		state.APIKeyHeader = types.StringValue("Authorization")
		plan := state
		plan.DisplayName = types.StringValue("MCP Renamed")

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, plan, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, request.DisplayName)
		require.Equal(t, "MCP Renamed", *request.DisplayName)
		// Unchanged fields must be omitted so a stale refreshed value cannot
		// overwrite an out-of-band server change during apply.
		require.Equal(t, codersdk.UpdateMCPServerConfigRequest{DisplayName: request.DisplayName}, request)
	})

	t.Run("unknown planned values are omitted", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("api_key")
		state.APIKeyHeader = types.StringValue("Authorization")
		plan := state
		plan.AuthType = types.StringValue("none")
		plan.APIKeyHeader = types.StringUnknown()

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, plan, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, request.AuthType)
		require.Equal(t, "none", *request.AuthType)
		require.Nil(t, request.APIKeyHeader)
	})

	t.Run("changed tool sets are decoded and sent", func(t *testing.T) {
		t.Parallel()
		state := testAgentsMCPServerModel("none")
		plan := state
		plan.ToolAllowList = stringSetValue([]string{"search"})

		var diags diag.Diagnostics
		request := plan.updateRequest(t.Context(), state, plan, &diags)
		require.False(t, diags.HasError(), diags.Errors())
		require.NotNil(t, request.ToolAllowList)
		require.Equal(t, []string{"search"}, *request.ToolAllowList)
		require.Nil(t, request.ToolDenyList)
	})
}

func TestAgentsMCPServerStateFromServer(t *testing.T) {
	t.Parallel()

	id := uuid.New()
	organizationID := uuid.New()
	createdAt := time.Unix(100, 0)
	updatedAt := time.Unix(200, 0)
	prior := testAgentsMCPServerModel("api_key")
	prior.APIKeyValueWOVersion = types.Int64Value(7)
	state := prior.stateFromServer(codersdk.MCPServerConfig{
		ID:                  id,
		OrganizationID:      organizationID,
		DisplayName:         "MCP Test",
		Slug:                "mcp-test",
		URL:                 "https://mcp.example.com/v1",
		Transport:           "streamable_http",
		AuthType:            "api_key",
		APIKeyHeader:        "Authorization",
		ToolAllowList:       []string{"search"},
		ToolDenyList:        []string{},
		Availability:        "default_on",
		Enabled:             true,
		ModelIntent:         true,
		AllowInPlanMode:     true,
		ForwardCoderHeaders: true,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
	})

	require.Equal(t, id, state.ID.ValueUUID())
	require.Equal(t, organizationID, state.OrganizationID.ValueUUID())
	require.Equal(t, "Authorization", state.APIKeyHeader.ValueString())
	// Optional strings the server omits ("") must normalize to null.
	require.True(t, state.OAuth2ClientID.IsNull())
	require.True(t, state.OAuth2AuthURL.IsNull())
	require.True(t, state.OAuth2TokenURL.IsNull())
	require.True(t, state.OAuth2RevocationURL.IsNull())
	require.Equal(t, prior.APIKeyValueWOVersion, state.APIKeyValueWOVersion)
	require.True(t, state.APIKeyValueWO.IsNull())
	require.Equal(t, int64(100), state.CreatedAt.ValueInt64())
	require.Equal(t, int64(200), state.UpdatedAt.ValueInt64())
	require.Len(t, state.ToolAllowList.Elements(), 1)
}

func testAgentsMCPServerModel(authType string) AgentsMCPServerResourceModel {
	return AgentsMCPServerResourceModel{
		DisplayName:                 types.StringValue("MCP Test"),
		Slug:                        types.StringValue("mcp-test"),
		URL:                         types.StringValue("https://mcp.example.com/v1"),
		Description:                 types.StringValue(""),
		IconURL:                     types.StringValue(""),
		Transport:                   types.StringValue("streamable_http"),
		AuthType:                    types.StringValue(authType),
		Availability:                types.StringValue("default_off"),
		OAuth2ClientID:              types.StringNull(),
		OAuth2ClientSecretWO:        types.StringNull(),
		OAuth2ClientSecretWOVersion: types.Int64Null(),
		OAuth2AuthURL:               types.StringNull(),
		OAuth2TokenURL:              types.StringNull(),
		OAuth2RevocationURL:         types.StringNull(),
		OAuth2Scopes:                types.StringValue(""),
		APIKeyHeader:                types.StringNull(),
		APIKeyValueWO:               types.StringNull(),
		APIKeyValueWOVersion:        types.Int64Null(),
		CustomHeadersWO:             types.MapNull(types.StringType),
		CustomHeadersWOVersion:      types.Int64Null(),
		ToolAllowList:               stringSetValue(nil),
		ToolDenyList:                stringSetValue(nil),
		Enabled:                     types.BoolValue(false),
		ModelIntent:                 types.BoolValue(false),
		AllowInPlanMode:             types.BoolValue(false),
		ForwardCoderHeaders:         types.BoolValue(false),
	}
}

func TestAccAgentsMCPServerResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "agents_mcp_server_acc")
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

	minimal := testAccAgentsMCPServerResourceConfig{
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

	// The server clears the header when auth_type leaves api_key, so keeping
	// it configured could never match the plan after apply.
	transitionKeepsHeader := noAuth
	transitionKeepsHeader.APIKeyHeader = "Authorization"

	transitionOAuth2Discovery := noAuth
	transitionOAuth2Discovery.AuthType = "oauth2"

	transitionOAuth2MissingSecret := noAuth
	transitionOAuth2MissingSecret.AuthType = "oauth2"
	transitionOAuth2MissingSecret.RawConfig = `  oauth2_client_id                = "client-id"
  oauth2_auth_url                 = "https://issuer.example.com/authorize"
  oauth2_token_url                = "https://issuer.example.com/token"
  oauth2_client_secret_wo_version = 1`

	transitionOAuth2UnknownEndpointMissingSecret := noAuth
	transitionOAuth2UnknownEndpointMissingSecret.AuthType = "oauth2"
	transitionOAuth2UnknownEndpointMissingSecret.RawConfig = `  oauth2_client_id                = "client-id"
  oauth2_auth_url                 = terraform_data.endpoint.output
  oauth2_token_url                = "https://issuer.example.com/token"
  oauth2_client_secret_wo_version = 1`

	// The endpoint is unknown at plan time and resolves to null at apply
	// time, so the deferred transition check must re-fire during the
	// apply-time re-plan, before the server is mutated.
	transitionOAuth2NullEndpoint := noAuth
	transitionOAuth2NullEndpoint.AuthType = "oauth2"
	transitionOAuth2NullEndpoint.RawConfig = `  oauth2_client_id = "client-id"
  oauth2_auth_url  = terraform_data.nullendpoint.id == "" ? "https://issuer.example.com/authorize" : null
  oauth2_token_url = "https://issuer.example.com/token"`

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   testAgentsMCPServerTerraformVersionChecks(),
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
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "display_name", minimal.DisplayName),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "slug", minimal.Slug),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "description", ""),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "icon_url", ""),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "transport", "streamable_http"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "auth_type", "none"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "availability", "default_off"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "enabled", "false"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "model_intent", "false"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "allow_in_plan_mode", "false"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "forward_coder_headers", "false"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "tool_allow_list.#", "0"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "tool_deny_list.#", "0"),
					resource.TestCheckResourceAttrSet("coderd_agents_mcp_server.test", "organization_id"),
					resource.TestCheckResourceAttrSet("coderd_agents_mcp_server.test", "created_at"),
					resource.TestCheckResourceAttrSet("coderd_agents_mcp_server.test", "updated_at"),
				),
			},
			{
				Config: updated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "display_name", updated.DisplayName),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "description", updated.Description),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "url", updated.ServerURL),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "enabled", "true"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "availability", updated.Availability),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "tool_allow_list.#", "2"),
					resource.TestCheckTypeSetElemAttr("coderd_agents_mcp_server.test", "tool_allow_list.*", "search"),
					resource.TestCheckTypeSetElemAttr("coderd_agents_mcp_server.test", "tool_allow_list.*", "read"),
					resource.TestCheckTypeSetElemAttr("coderd_agents_mcp_server.test", "tool_deny_list.*", "delete"),
				),
			},
			{
				Config: renamed.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_agents_mcp_server.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "slug", renamed.Slug),
			},
			{
				Config: apiKey.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "api_key_header", apiKey.APIKeyHeader),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "api_key_value_wo_version", "1"),
					resource.TestCheckNoResourceAttr("coderd_agents_mcp_server.test", "api_key_value_wo"),
					checkAgentsMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config: rotated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "api_key_value_wo_version", "2"),
					checkAgentsMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config:      rotationMissingSecret.String(t),
				ExpectError: regexp.MustCompile("Missing API Key Value"),
			},
			{
				Config: noAuth.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "auth_type", "none"),
					resource.TestCheckNoResourceAttr("coderd_agents_mcp_server.test", "api_key_header"),
					checkAgentsMCPServerAPIKey(ctx, t, client, false),
				),
			},
			{
				Config:      transitionMissingHeader.String(t),
				ExpectError: regexp.MustCompile("Missing API Key Header"),
			},
			{
				Config:      transitionKeepsHeader.String(t),
				ExpectError: regexp.MustCompile("Invalid Attribute Combination"),
			},
			{
				Config:      transitionOAuth2Discovery.String(t),
				ExpectError: regexp.MustCompile("Missing OAuth2 Configuration"),
			},
			{
				Config:      transitionOAuth2MissingSecret.String(t),
				ExpectError: regexp.MustCompile("Missing OAuth2 Client Secret"),
			},
			{
				// A not-yet-known endpoint defers only its own check, so the
				// independent secret-rotation check must still fail the plan.
				// PlanOnly guards the timing: the apply-time backstop reports
				// the same error, so a full apply step would pass either way.
				Config: transitionOAuth2UnknownEndpointMissingSecret.String(t) + `
resource "terraform_data" "endpoint" {
  input = "https://issuer.example.com/authorize"
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile("Missing OAuth2 Client Secret"),
			},
			{
				Config: transitionOAuth2NullEndpoint.String(t) + `
resource "terraform_data" "nullendpoint" {
}
`,
				ExpectError: regexp.MustCompile("(Missing|Incomplete) OAuth2 Configuration"),
			},
			{
				Config: rotated.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "api_key_header", rotated.APIKeyHeader),
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "api_key_value_wo_version", "2"),
					checkAgentsMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				Config: adopted.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_agents_mcp_server.test", "auth_type", "api_key"),
					resource.TestCheckNoResourceAttr("coderd_agents_mcp_server.test", "api_key_value_wo_version"),
					checkAgentsMCPServerAPIKey(ctx, t, client, true),
				),
			},
			{
				ResourceName:      "coderd_agents_mcp_server.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["coderd_agents_mcp_server.test"]
					if !ok {
						return "", fmt.Errorf("coderd_agents_mcp_server.test not found in state")
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

type testAccAgentsMCPServerResourceConfig struct {
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
	RawConfig          string
}

func (c testAccAgentsMCPServerResourceConfig) String(t *testing.T) string {
	t.Helper()
	const tpl = `
provider "coderd" {
  url   = "{{.URL}}"
  token = "{{.Token}}"
}

resource "coderd_agents_mcp_server" "test" {
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
{{- if .RawConfig }}
{{.RawConfig}}
{{- end }}
}
`
	var out bytes.Buffer
	require.NoError(t, template.Must(template.New("mcpServerResource").Parse(tpl)).Execute(&out, c))
	return out.String()
}

func checkAgentsMCPServerAPIKey(ctx context.Context, t *testing.T, client *codersdk.Client, want bool) resource.TestCheckFunc {
	t.Helper()
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["coderd_agents_mcp_server.test"]
		if !ok {
			return fmt.Errorf("coderd_agents_mcp_server.test not found in state")
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
