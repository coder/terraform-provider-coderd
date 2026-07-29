package provider

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test case coverage for the OAuth2 provider settings resource. The TC numbers
// refer to the ENG-3083 proposal, section 5.
//
// Two of the proposal's cases are deliberately not implemented here, because
// neither is observable from inside this repository's test process:
//
//   - TC10 (provider version predates the feature) is a property of which
//     compiled provider binary Terraform loads. Every test in this package
//     links the current package, so the resource type always exists. The
//     failure mode ("Invalid resource type") is produced by Terraform core
//     before any provider code runs, not by anything this repo can regress.
//   - TC12 (Coder upgraded by a `helm_release` in the same apply, no
//     `depends_on`) depends on Terraform's graph walk across a third-party
//     provider. Its documented outcome is that an unordered apply collapses
//     into TC11, and TC11 is covered below: the 404 surfaces as one clean,
//     actionable error rather than a hang or a corrupted state entry.
//
// Both are documented on the resource's schema instead.

const oauth2SettingsResourceAddr = "coderd_oauth2_provider_settings.test"

func oauth2SettingsProviderBlock(url string) string {
	return fmt.Sprintf(`
provider "coderd" {
	url   = %q
	token = "test-token"
}
`, url)
}

// oauth2SettingsConfig renders a provider block plus a single settings
// resource.
func oauth2SettingsConfig(url string, dcrEnabled bool) string {
	return oauth2SettingsProviderBlock(url) + fmt.Sprintf(`
resource "coderd_oauth2_provider_settings" "test" {
	dynamic_client_registration_enabled = %t
}
`, dcrEnabled)
}

// TestDCREnabledOrDefault covers the nil-handling forced by
// `DynamicClientRegistrationEnabled` being a `*bool`. The pointer exists so a
// PUT can omit the field to mean "leave this alone"; on a GET the field is
// documented to always come back non-nil, so the nil branch is defensive
// against a contract violation rather than an expected response.
func TestDCREnabledOrDefault(t *testing.T) {
	t.Parallel()

	enabled, disabled := true, false

	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{
		{name: "True", in: &enabled, want: true},
		{name: "False", in: &disabled, want: false},
		{
			// Never expected from a GET. Falls back to the same default the
			// server applies to a never-configured deployment, rather than
			// panicking or inventing a value.
			name: "NilFallsBackToDeploymentDefault",
			in:   nil,
			want: oauth2ProviderSettingsDefaultDCR,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dcrEnabledOrDefault(codersdk.OAuth2ProviderSettings{
				DynamicClientRegistrationEnabled: tc.in,
			})
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestOAuth2ProviderSettingsDiag covers the three branches of the shared
// diagnostic helper: the 404 version hint, and everything else falling through to
// a generic client error that preserves coderd's own message.
//
// There is deliberately no branch for the `oauth2` experiment gate. coderd
// answers 403 for two unrelated reasons -- an RBAC denial and the experiment gate
// -- and telling them apart client-side would mean matching on message text
// copied from coderd's middleware. That match can silently become a no-op when
// the wording changes upstream, and the test guarding it would keep passing,
// because the test supplies the same copied string. Passing the message through
// is just as actionable and cannot rot. A richer remedy needs a machine-readable
// error code from coderd, not string matching here.
func TestOAuth2ProviderSettingsDiag(t *testing.T) {
	t.Parallel()

	// A 404 is the one status worth special-casing: it means the endpoint is
	// absent, which is a version problem with an actionable fix, and coderd's
	// own "Not Found." says nothing useful about that.
	versionDiags := oauth2ProviderSettingsDiag("read",
		codersdk.NewError(http.StatusNotFound, codersdk.Response{Message: "Not Found."}))
	require.Len(t, versionDiags, 1)
	assert.Equal(t, "Unsupported Coder Version", versionDiags[0].Summary())
	assert.Contains(t, versionDiags[0].Detail(), oauth2ProviderSettingsMinVersion)

	// Every 403 is a generic client error, whatever its cause, and coderd's
	// message must survive into the detail. Both cases below assert the same
	// property, which is the whole point of not classifying: the provider treats
	// them identically and lets the server explain itself.
	for _, tc := range []struct {
		name    string
		message string
	}{
		{
			// An RBAC denial, as TC13/TC23 produce.
			name:    "RBACDenial",
			message: "Forbidden.",
		},
		{
			// The experiment gate. The string is deliberately synthetic rather
			// than a copy of coderd's wording: nothing here depends on the exact
			// text, so there is nothing to keep in sync.
			name:    "ExperimentGate",
			message: "<some 403 explaining the experiment is off>",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags := oauth2ProviderSettingsDiag("update",
				codersdk.NewError(http.StatusForbidden, codersdk.Response{Message: tc.message}))
			require.Len(t, diags, 1)
			assert.Equal(t, "Client Error", diags[0].Summary())
			assert.Contains(t, diags[0].Detail(), tc.message,
				"coderd's own message must reach the user, since the provider adds no interpretation")
			assert.Contains(t, diags[0].Detail(), "unable to update")
		})
	}

	// A non-SDK error still produces one clean diagnostic rather than panicking.
	netDiags := oauth2ProviderSettingsDiag("create", errors.New("dial tcp: connection refused"))
	require.Len(t, netDiags, 1)
	assert.Equal(t, "Client Error", netDiags[0].Summary())
	assert.Contains(t, netDiags[0].Detail(), "connection refused")
}

// TestOAuth2ProviderSettingsModifyPlan covers the plan-time advisory that
// makes TC3's overwrite visible before anything is applied. A plain unit test
// rather than a TestAcc one: the assertion is on the diagnostics ModifyPlan
// emits, which needs no Terraform binary, and the test framework at this
// version has no way to match a warning in plan output.
func TestOAuth2ProviderSettingsModifyPlan(t *testing.T) {
	t.Parallel()

	objType := tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"dynamic_client_registration_enabled": tftypes.Bool,
		},
	}
	// boolObject builds a state/plan value. A nil `v` yields a null object,
	// which is how the framework signals "no prior state" (a create) and "no
	// plan" (a destroy).
	boolObject := func(v any) tftypes.Value {
		if v == nil {
			return tftypes.NewValue(objType, nil)
		}
		return tftypes.NewValue(objType, map[string]tftypes.Value{
			"dynamic_client_registration_enabled": tftypes.NewValue(tftypes.Bool, v),
		})
	}
	nullObject := boolObject(nil)

	for _, tc := range []struct {
		name string
		// live is the deployment's current value.
		live bool
		// lookupStatus, when non-zero, makes the settings GET fail.
		lookupStatus int
		plan         tftypes.Value
		state        tftypes.Value
		wantWarnings int
		wantGets     int
	}{
		{
			// The TC3 scenario: DCR is on, the first apply would turn it off.
			name:         "WarnsWhenFirstApplyWouldDisableLiveEnabled",
			live:         true,
			plan:         boolObject(false),
			state:        nullObject,
			wantWarnings: 1,
			wantGets:     1,
		},
		{
			// A never-configured deployment reads back as false, so warning
			// here would fire on every greenfield apply.
			name:         "SilentWhenEnablingOnGreenfieldDeployment",
			live:         false,
			plan:         boolObject(true),
			state:        nullObject,
			wantWarnings: 0,
			wantGets:     0, // enabling is never destructive, so no lookup
		},
		{
			name:         "SilentWhenPlanAlreadyMatchesLiveValue",
			live:         true,
			plan:         boolObject(true),
			state:        nullObject,
			wantWarnings: 0,
			wantGets:     0,
		},
		{
			name:         "SilentWhenDisablingAlreadyDisabled",
			live:         false,
			plan:         boolObject(false),
			state:        nullObject,
			wantWarnings: 0,
			wantGets:     1, // looked up, found nothing worth reporting
		},
		{
			// Not a create. An update already renders a real diff, and this is
			// also the first plan after `terraform import`.
			name:         "SilentWhenPriorStateExists",
			live:         true,
			plan:         boolObject(false),
			state:        boolObject(true),
			wantWarnings: 0,
			wantGets:     0,
		},
		{
			name:         "SilentOnDestroyPlan",
			live:         true,
			plan:         nullObject,
			state:        boolObject(true),
			wantWarnings: 0,
			wantGets:     0,
		},
		{
			// A Required attribute is still unknown when it comes from an
			// input variable or module output.
			name:         "SilentWhenPlannedValueUnknown",
			live:         true,
			plan:         boolObject(tftypes.UnknownValue),
			state:        nullObject,
			wantWarnings: 0,
			wantGets:     0,
		},
		{
			// Best-effort: a failed lookup must not turn into a plan error.
			// Create() makes the same call and reports it properly.
			name:         "SilentWhenLookupFails",
			live:         true,
			lookupStatus: http.StatusForbidden,
			plan:         boolObject(false),
			state:        nullObject,
			wantWarnings: 0,
			wantGets:     1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			f := newFakeCoderd(t)
			f.SetDCREnabled(tc.live)
			if tc.lookupStatus != 0 {
				f.SetGetStatus(tc.lookupStatus)
			}

			serverURL, err := url.Parse(f.URL)
			require.NoError(t, err)
			client := codersdk.New(serverURL)
			client.SetSessionToken("test-token")

			r := &OAuth2ProviderSettingsResource{
				CoderdProviderData: &CoderdProviderData{Client: client},
			}

			schemaResp := &fwresource.SchemaResponse{}
			r.Schema(ctx, fwresource.SchemaRequest{}, schemaResp)
			require.Empty(t, schemaResp.Diagnostics)
			s := schemaResp.Schema

			resp := &fwresource.ModifyPlanResponse{Plan: tfsdk.Plan{Schema: s, Raw: tc.plan}}
			r.ModifyPlan(ctx, fwresource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: s, Raw: tc.plan},
				Plan:   tfsdk.Plan{Schema: s, Raw: tc.plan},
				State:  tfsdk.State{Schema: s, Raw: tc.state},
			}, resp)

			assert.Empty(t, resp.Diagnostics.Errors(), "a plan-time advisory must never fail the plan")
			assert.Len(t, resp.Diagnostics.Warnings(), tc.wantWarnings)
			assert.Equal(t, tc.wantGets, f.SettingsRequestCount(http.MethodGet))
		})
	}
}

func TestAccOAuth2ProviderSettingsResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	// TC2 — First Create against a never-configured deployment.
	t.Run("TC2_CreateAgainstNeverConfigured", func(t *testing.T) {
		f := newFakeCoderd(t)
		// Live value: never configured, which the server coalesces to false.
		require.False(t, f.DCREnabled())

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(oauth2SettingsResourceAddr, plancheck.ResourceActionCreate),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsResourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(true)),
					},
					Check: func(*terraform.State) error {
						assert.True(t, f.DCREnabled(), "live value should have been enabled")
						return nil
					},
				},
			},
		})
	})

	// TC3 — First Create against a deployment already configured out of band,
	// without importing first. This asserts the footgun exists, so it stays a
	// documented guarantee rather than an accident: the live value is flipped
	// with no diff and, because there is no prior state, no way for Terraform
	// to render it as a change.
	//
	// The overwrite itself still happens; ModifyPlan makes it *visible* at plan
	// time rather than preventing it (see
	// TestOAuth2ProviderSettingsModifyPlan/WarnsWhenFirstApplyWouldDisableLiveEnabled).
	// An apply is not blocked by a warning, so the guarantee asserted here is
	// unchanged.
	t.Run("TC3_CreateOverwritesOutOfBandValue", func(t *testing.T) {
		f := newFakeCoderd(t)
		// Enabled previously via `coder oauth2-provider dcr enable` or the UI.
		f.SetDCREnabled(true)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, false),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							// A Create, not an Update: there is no prior state
							// to diff the live `true` against.
							plancheck.ExpectResourceAction(oauth2SettingsResourceAddr, plancheck.ResourceActionCreate),
						},
					},
					Check: func(*terraform.State) error {
						assert.False(t, f.DCREnabled(), "live value should have been silently overwritten to false")
						puts := f.SettingsRequests()
						require.NotEmpty(t, puts)
						last := puts[len(puts)-1]
						assert.Equal(t, http.MethodPut, last.Method)
						assert.False(t, last.DCREnabled)
						return nil
					},
				},
			},
		})
	})

	// TC4 — Declared but the required attribute is omitted. Terraform's own
	// schema validation rejects this before Configure() runs, so no request is
	// ever made.
	t.Run("TC4_RequiredAttributeOmitted", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsProviderBlock(f.URL) + `
resource "coderd_oauth2_provider_settings" "test" {
}
`,
					ExpectError: regexp.MustCompile(`The argument "dynamic_client_registration_enabled" is required`),
				},
			},
		})

		assert.Empty(t, f.SettingsRequests(), "config validation must fail before any API call")
	})

	// TC5 — Drift detection: live value changed out of band after a successful
	// apply.
	t.Run("TC5_DriftDetection", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
				},
				{
					// Another actor flips the value via the CLI or API.
					PreConfig:          func() { f.SetDCREnabled(false) },
					Config:             oauth2SettingsConfig(f.URL, true),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})

	// TC6 — No-diff refresh when the never-configured default matches config.
	t.Run("TC6_NoDiffWhenDefaultMatches", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, false),
				},
				{
					// The harness fails a PlanOnly step on any diff unless
					// ExpectNonEmptyPlan is set, so this step passing is the
					// assertion.
					Config:   oauth2SettingsConfig(f.URL, false),
					PlanOnly: true,
				},
			},
		})
	})

	// TC7 — Update flips the value with no resource replacement.
	t.Run("TC7_UpdateInPlace", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
				},
				{
					Config: oauth2SettingsConfig(f.URL, false),
					ConfigPlanChecks: resource.ConfigPlanChecks{
						PreApply: []plancheck.PlanCheck{
							plancheck.ExpectResourceAction(oauth2SettingsResourceAddr, plancheck.ResourceActionUpdate),
						},
					},
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsResourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(false)),
					},
					Check: func(*terraform.State) error {
						assert.False(t, f.DCREnabled())
						return nil
					},
				},
			},
		})
	})

	// TC8 — Destroy resets to the documented default.
	t.Run("TC8_DestroyResetsToDefault", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			CheckDestroy: func(*terraform.State) error {
				// The API has no DELETE verb, so Delete() resets to the
				// deployment default via the same PUT.
				if f.DCREnabled() {
					return fmt.Errorf("expected destroy to reset the live value to false")
				}
				reqs := f.SettingsRequests()
				last := reqs[len(reqs)-1]
				if last.Method != http.MethodPut || last.DCREnabled {
					return fmt.Errorf("expected the final request to be a PUT of false, got %+v", last)
				}
				// The field must be explicitly present. The API reads an
				// omitted field as "leave the current value alone", so a
				// regression that stopped sending it would turn this reset
				// into a silent no-op while every other assertion still
				// passed.
				if !last.DCRSent {
					return fmt.Errorf("destroy must send dynamic_client_registration_enabled explicitly, not omit it")
				}
				return nil
			},
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					Check: func(*terraform.State) error {
						assert.True(t, f.DCREnabled())
						return nil
					},
				},
			},
		})
	})

	// TC9 — Destroy fails cleanly on a transient error: the failure is
	// surfaced and the resource stays in state, so a retry is possible.
	t.Run("TC9_DestroyFailsCleanly", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
				},
				{
					PreConfig: func() { f.SetPutStatus(http.StatusInternalServerError) },
					Config:    oauth2SettingsConfig(f.URL, true),
					Destroy:   true,
					// "reset", not "update": every write path shares one PUT,
					// but the diagnostic names the operation that failed.
					ExpectError: regexp.MustCompile(`unable to reset OAuth2 provider settings`),
				},
				{
					// The live value was never reset, and the resource is
					// still in state: this step is a no-op apply, which the
					// harness only accepts if both are true. (A dropped state
					// entry would re-Create; a reset live value would diff.)
					PreConfig: func() { f.SetPutStatus(0) },
					Config:    oauth2SettingsConfig(f.URL, true),
					Check: func(*terraform.State) error {
						assert.True(t, f.DCREnabled(), "a failed destroy must not have changed the live value")
						return nil
					},
				},
			},
		})
	})

	// TC11 — New provider, old Coderd (pre-#27316): the endpoint 404s and the
	// provider must name the minimum version rather than leaking a decode
	// failure.
	t.Run("TC11_OldCoderd", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetGetStatus(http.StatusNotFound)
		f.SetPutStatus(http.StatusNotFound)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      oauth2SettingsConfig(f.URL, true),
					ExpectError: regexp.MustCompile(`Unsupported Coder Version`),
				},
			},
		})
	})

	// TC13 — Token holding neither read nor update on the deployment config
	// (e.g. a plain Member). The provider makes no client-side authorization
	// decision; it surfaces whatever the API returned.
	t.Run("TC13_Forbidden", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetGetStatus(http.StatusForbidden)
		f.SetPutStatus(http.StatusForbidden)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					// "create": this is a first apply with no prior state.
					ExpectError: regexp.MustCompile(`(?s)unable to create OAuth2 provider settings.*Forbidden`),
				},
			},
		})
	})

	// Beyond the proposal: the deployment has the `oauth2` experiment disabled,
	// which is the default for a stock release deployment. Route middleware
	// refuses both verbs with a 403 before any handler runs, so the setting cannot
	// be changed — the behaviour we want — and coderd's own message names the
	// experiment, so the admin is told which knob to turn without the provider
	// interpreting anything. This test asserts that pass-through rather than a
	// provider-authored remedy; see TestOAuth2ProviderSettingsDiag for why.
	//
	// Not reachable through `scripts/develop.sh`: development builds bypass the
	// experiment check (`RequireExperimentWithDevBypass`), so a fake is the only
	// way to exercise this short of a real release binary.
	t.Run("ExperimentDisabled", func(t *testing.T) {
		f := newFakeCoderd(t)
		// Synthetic wording on purpose: the assertion is that coderd's message
		// reaches the user, not that the provider recognises any particular text.
		const serverMessage = "<some 403 explaining the experiment is off>"
		f.SetForbiddenWithMessage(serverMessage)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					// The server's own explanation is surfaced verbatim; the
					// provider adds only which operation failed.
					ExpectError: regexp.MustCompile(`(?s)unable to create OAuth2 provider settings.*` + regexp.QuoteMeta(serverMessage)),
				},
			},
		})

		// The refusal must be total: nothing was written, so a deployment with
		// the experiment off cannot end up half-configured.
		assert.False(t, f.DCREnabled(), "a refused apply must not have changed the live value")
	})

	// TC14 — Concurrent external mutation mid-apply. There is no optimistic
	// concurrency control (no ETag or version field), so the last PUT to land
	// wins and Terraform cannot detect the collision. Documented limitation,
	// asserted so it stays documented.
	t.Run("TC14_ConcurrentExternalMutationLastWriteWins", func(t *testing.T) {
		f := newFakeCoderd(t)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
				},
				{
					PreConfig: func() {
						// Another actor writes `true` in the window between
						// this apply's refresh and its PUT.
						f.SetBeforeGet(nil)
						f.mu.Lock()
						f.beforePut = func(f *fakeCoderd) { f.SetDCREnabled(true) }
						f.mu.Unlock()
					},
					Config: oauth2SettingsConfig(f.URL, false),
					Check: func(*terraform.State) error {
						// Terraform's write landed last, so it wins. No error,
						// no detection.
						assert.False(t, f.DCREnabled())
						return nil
					},
				},
			},
		})
	})

	// TC15 — Two resource blocks targeting the same singleton. This is not an
	// error: the API has no "already exists" concept, so the blocks silently
	// fight, leaving a permanent diff.
	t.Run("TC15_TwoBlocksSameSingleton", func(t *testing.T) {
		f := newFakeCoderd(t)

		cfg := oauth2SettingsProviderBlock(f.URL) + `
resource "coderd_oauth2_provider_settings" "a" {
	dynamic_client_registration_enabled = true
}

resource "coderd_oauth2_provider_settings" "b" {
	dynamic_client_registration_enabled = false
}
`

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg,
					// Whichever block applied second overwrote the other, so
					// the loser refreshes to a value that disagrees with its
					// config, forever.
					ExpectNonEmptyPlan: true,
					Check: func(*terraform.State) error {
						assert.Equal(t, 2, f.SettingsRequestCount(http.MethodPut),
							"both blocks should have written, with no collision error")
						return nil
					},
				},
			},
		})
	})

	// State is built from the value the PUT reports back, not from the plan.
	// The two agree on a real deployment, so the only way to tell them apart is
	// to make the fake disagree. Both cases below fail if `patch()` goes back to
	// discarding the response.
	t.Run("StateComesFromThePutResponse", func(t *testing.T) {
		f := newFakeCoderd(t)
		// The deployment stores `true` as asked but claims `false`, as an API
		// that had started normalizing the value would.
		f.SetPutEcho(false)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					// The attribute is Required, so Terraform demands final
					// state equal the planned value. Reporting the server's
					// answer therefore surfaces the divergence as a hard error
					// rather than leaving a falsehood in state to be found as
					// drift on a later plan. Sourcing state from the plan
					// instead would apply cleanly and hide it.
					ExpectError: regexp.MustCompile(`(?s)Provider produced inconsistent result after apply`),
				},
			},
		})
	})

	// The nil branch of that same read. `dcrEnabledOrDefault` would coerce an
	// absent field to `false` and turn this successful apply into the error
	// asserted above; falling back to the value just sent is what keeps
	// Terraform's plan-equals-state contract intact.
	t.Run("AbsentPutResponseFieldFallsBackToThePlanValue", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetPutEchoOmit()

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: oauth2SettingsConfig(f.URL, true),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue(oauth2SettingsResourceAddr,
							tfjsonpath.New("dynamic_client_registration_enabled"), knownvalue.Bool(true)),
					},
					Check: func(*terraform.State) error {
						assert.True(t, f.DCREnabled(), "the value sent should still have been stored")
						return nil
					},
				},
			},
		})
	})

	// TC21 — Adopting a deployment via `terraform import`, the safe
	// alternative to TC3's bare first apply.
	t.Run("TC21_ImportAdoptsLiveValue", func(t *testing.T) {
		f := newFakeCoderd(t)
		// Already enabled out of band, the same starting point as TC3.
		f.SetDCREnabled(true)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					// The admin's desired value differs from what is live.
					Config:             oauth2SettingsConfig(f.URL, false),
					ResourceName:       oauth2SettingsResourceAddr,
					ImportState:        true,
					ImportStateId:      "oauth2_provider_settings",
					ImportStatePersist: true,
					ImportStateCheck: func(states []*terraform.InstanceState) error {
						if len(states) != 1 {
							return fmt.Errorf("expected 1 imported instance, got %d", len(states))
						}
						if got := states[0].Attributes["dynamic_client_registration_enabled"]; got != "true" {
							return fmt.Errorf("expected the imported state to hold the live value true, got %q", got)
						}
						return nil
					},
					Check: func(*terraform.State) error {
						// The core guarantee: import reads, it never writes.
						assert.Zero(t, f.SettingsRequestCount(http.MethodPut),
							"import must never call PutOAuth2ProviderSettings")
						assert.True(t, f.DCREnabled(), "import must not disturb the live value")
						return nil
					},
				},
				{
					// Now the admin gets an honest, reviewable diff instead of
					// TC3's silent overwrite.
					Config:             oauth2SettingsConfig(f.URL, false),
					PlanOnly:           true,
					ExpectNonEmptyPlan: true,
				},
			},
		})
	})

	// TC23 — Auditor-role token: the GET succeeds, the PUT is forbidden. A
	// genuinely distinct outcome from both TC2 (all succeed) and TC13 (all
	// fail), on one token.
	t.Run("TC23_ReadOkWriteForbidden", func(t *testing.T) {
		f := newFakeCoderd(t)
		f.SetDCREnabled(true)
		f.SetPutStatus(http.StatusForbidden)

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					// Import exercises the read path alone, so this step
					// succeeding proves the GET is permitted.
					Config:             oauth2SettingsConfig(f.URL, false),
					ResourceName:       oauth2SettingsResourceAddr,
					ImportState:        true,
					ImportStateId:      "oauth2_provider_settings",
					ImportStatePersist: true,
					ImportStateCheck: func(states []*terraform.InstanceState) error {
						if got := states[0].Attributes["dynamic_client_registration_enabled"]; got != "true" {
							return fmt.Errorf("expected read to succeed and yield true, got %q", got)
						}
						return nil
					},
				},
				{
					// The write path, on the same token, is refused.
					Config:      oauth2SettingsConfig(f.URL, false),
					ExpectError: regexp.MustCompile(`(?s)unable to update OAuth2 provider settings.*Forbidden`),
				},
				{
					// Restore write access purely so the harness can destroy.
					PreConfig: func() { f.SetPutStatus(0) },
					Config:    oauth2SettingsConfig(f.URL, false),
				},
			},
		})
	})
}

// TestAccOAuth2ProviderSettingsNotDeclared covers the proposal's 5.1 group:
// declining to manage this setting is always safe, and costs nothing but
// omitting the block.
func TestAccOAuth2ProviderSettingsNotDeclared(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}

	// unrelatedConfig stands in for a configuration that manages other things
	// and has no interest in OAuth2 or DCR. `terraform_data` is a Terraform
	// builtin, so this exercises an apply that touches no coderd resource at
	// all.
	unrelatedConfig := func(url, value string) string {
		return oauth2SettingsProviderBlock(url) + fmt.Sprintf(`
resource "terraform_data" "unrelated" {
	input = %q
}
`, value)
	}

	// TC1 — The resource is simply not declared. Nothing plans, nothing
	// applies, nothing is called.
	// TC20 — The same, while making an unrelated change: shipping a new
	// resource type does not retroactively require existing configs to declare
	// it, so the `Required` attribute is never evaluated.
	// TC22 — The same again, with the live value already `true`, to isolate
	// where TC3's risk actually comes from: declaring the resource with an
	// unverified value, not declining to manage it.
	for _, tc := range []struct {
		name    string
		liveDCR bool
	}{
		{name: "TC1_TC20_NotDeclared", liveDCR: false},
		{name: "TC22_NotDeclaredLiveTrue", liveDCR: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeCoderd(t)
			f.SetDCREnabled(tc.liveDCR)

			resource.Test(t, resource.TestCase{
				IsUnitTest:               true,
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: unrelatedConfig(f.URL, "before")},
					{Config: unrelatedConfig(f.URL, "after")},
				},
			})

			assert.Empty(t, f.SettingsRequests(),
				"an apply that does not declare the resource must never touch the settings endpoint")
			assert.Equal(t, tc.liveDCR, f.DCREnabled(), "the live value must be undisturbed")
		})
	}
}
