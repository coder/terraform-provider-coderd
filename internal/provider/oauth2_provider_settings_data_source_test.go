package provider

import (
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
)

const oauth2SettingsDataSourceAddr = "data.coderd_oauth2_provider_settings.test"

func oauth2SettingsDataSourceConfig(url string) string {
	return oauth2SettingsProviderBlock(url) + `
data "coderd_oauth2_provider_settings" "test" {}
`
}

// TestAccOAuth2ProviderSettingsDataSource covers the proposal's 5.7 group: the
// read-without-owning path.
func TestAccOAuth2ProviderSettingsDataSource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	// TC16 — Data source only, no resource declared. The invariant that makes
	// it safe to declare alongside a resource owned elsewhere is that it never
	// writes, asserted here against the test double rather than claimed in
	// docs.
	t.Run("TC16_ReadOnlyNeverPuts", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetDCREnabled(true)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsDataSourceConfig(f.URL),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsDataSourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(true)),
					},
				},
				{
					// Repeated applies must stay read-only too.
					Config: oauth2SettingsDataSourceConfig(f.URL),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsDataSourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(true)),
					},
				},
			},
		})

		assert.Zero(t, f.SettingsRequestCount(http.MethodPut), "the data source must never issue a PUT")
		assert.NotZero(t, f.SettingsRequestCount(http.MethodGet))
		assert.True(t, f.DCREnabled(), "the live value must be untouched")
	})

	// TC17 — Data source with a token that lacks read on the deployment
	// config: the 403 surfaces as an error, not a silently zero-valued
	// attribute.
	t.Run("TC17_Forbidden", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetGetStatus(http.StatusForbidden)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      oauth2SettingsDataSourceConfig(f.URL),
					ExpectError: regexp.MustCompile(`(?s)unable to read OAuth2 provider settings.*Forbidden`),
				},
			},
		})
	})

	// Beyond the proposal: the `oauth2` experiment is disabled. The experiment
	// gates the whole /api/v2/oauth2-provider route, not just its write path, so
	// read-only access is no escape hatch — this is the one respect in which the
	// data source is *not* more permissive than the resource. Worth asserting
	// separately from TC17, since a reader who can work around an RBAC denial by
	// switching to the data source cannot work around this.
	t.Run("ExperimentDisabled", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetExperimentOff()

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      oauth2SettingsDataSourceConfig(f.URL),
					ExpectError: regexp.MustCompile(`(?s)OAuth2 Experiment Not Enabled.*CODER_EXPERIMENTS=oauth2`),
				},
			},
		})
	})

	// TC18 — Data source against an old Coderd: the same 404-derived error as
	// TC11, since both route through Client.OAuth2ProviderSettings.
	t.Run("TC18_OldCoderd", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetGetStatus(http.StatusNotFound)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      oauth2SettingsDataSourceConfig(f.URL),
					ExpectError: regexp.MustCompile(`Unsupported Coder Version`),
				},
			},
		})
	})

	// TC19 — Fresh deployment, never configured, observed through the data
	// source only. The value reaches state even though no configuration
	// anywhere supplied one, because the data source's only attribute is
	// Computed.
	t.Run("TC19_FreshDeploymentDefaultsFalse", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsDataSourceConfig(f.URL),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsDataSourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(false)),
					},
					Check: func(*terraform.State) error {
						assert.Zero(t, f.SettingsRequestCount(http.MethodPut))
						return nil
					},
				},
			},
		})
	})

	// TC24 — Auditor-role token: the data source resolves fine even though the
	// same token could not own the equivalent resource (TC23). The data source
	// only ever calls the read-gated GET, so the write-gated 403 is never
	// reached.
	t.Run("TC24_ReadableByTokenThatCannotWrite", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetDCREnabled(true)
		f.SetPutStatus(http.StatusForbidden)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsDataSourceConfig(f.URL),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsDataSourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(true)),
					},
				},
			},
		})

		assert.Zero(t, f.SettingsRequestCount(http.MethodPut),
			"the forbidden write path must never be exercised by the data source")
	})
}
