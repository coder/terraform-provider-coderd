package provider

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

func TestAccUserResource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "user_acc")

	cfg1 := testAccUserResourceConfig{
		URL:       client.URL.String(),
		Token:     client.SessionToken(),
		Username:  new("example"),
		Name:      new("Example User"),
		Email:     new("example@coder.com"),
		Roles:     new([]string{"owner", "auditor"}),
		LoginType: new("password"),
		Password:  new("SomeSecurePassword!"),
	}

	cfg2 := cfg1
	cfg2.Username = new("exampleNew")

	cfg3 := cfg2
	cfg3.Name = new("Example New")

	cfgEmailChanged := cfg3
	cfgEmailChanged.Email = new("example-new@coder.com")

	cfg4 := cfgEmailChanged
	cfg4.LoginType = new("github")
	cfg4.Password = nil

	cfg5 := cfg4
	cfg5.Roles = nil

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: cfg1.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "example"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", "example@coder.com"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.#", "2"),
					resource.TestCheckTypeSetElemAttr("coderd_user.test", "roles.*", "auditor"),
					resource.TestCheckTypeSetElemAttr("coderd_user.test", "roles.*", "owner"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("coderd_user.test", "password", "SomeSecurePassword!"),
					resource.TestCheckResourceAttr("coderd_user.test", "suspended", "false"),
				),
			},
			// Import by ID
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				// We can't pull the password from the API.
				ImportStateVerifyIgnore: []string{"password", "roles"},
			},
			// ImportState by username
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     "example",
				// We can't pull the password from the API.
				ImportStateVerifyIgnore: []string{"password", "roles"},
			},
			// Update and Read testing
			{
				Config: cfg2.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User"),
				),
			},
			{
				Config: cfg3.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example New"),
				),
			},
			// Email is immutable server-side, so changing it must replace the user
			// instead of writing the planned value into state and drifting on refresh.
			{
				Config: cfgEmailChanged.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_user.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "exampleNew"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example New"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", "example-new@coder.com"),
				),
			},
			// Replace triggered
			{
				Config: cfg4.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "github"),
				),
			},
			// Verify config drift via deletion is handled
			{
				Config: cfg4.String(t),
				Check: func(*terraform.State) error {
					user, err := client.User(ctx, "exampleNew")
					if err != nil {
						return err
					}
					return client.DeleteUser(ctx, user.ID)
				},
				// The Plan should be to create the entire resource
				ExpectNonEmptyPlan: true,
			},
			// Unmanaged roles
			{
				Config: cfg5.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("coderd_user.test", "roles"),
				),
			},
		},
	})

	t.Run("CreateUnmanagedRolesOk", func(t *testing.T) {
		cfg := testAccUserResourceConfig{
			URL:       client.URL.String(),
			Token:     client.SessionToken(),
			Username:  new("unmanaged"),
			Name:      new("Unmanaged User"),
			Email:     new("unmanaged@coder.com"),
			Roles:     nil, // Start with unmanaged roles
			LoginType: new("password"),
			Password:  new("SomeSecurePassword!"),
		}

		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckNoResourceAttr("coderd_user.test", "roles"),
					),
				},
			},
		})
	})
}

func TestAccUserResourceServiceAccount(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	// Service accounts are a Premium feature, so a licensed deployment is required.
	client := integration.StartCoder(ctx, t, "user_service_account_acc", integration.UseLicense)

	cfg := testAccUserResourceConfig{
		URL:              client.URL.String(),
		Token:            client.SessionToken(),
		Username:         new("service-account"),
		Name:             new("Service Account"),
		Roles:            new([]string{"template-admin"}),
		IsServiceAccount: new(true),
	}

	// Changing an unrelated attribute (name) on a service account exercises the
	// UseStateForUnknown plan modifier on `email`: without it the planned value
	// for `email` would be "(known after apply)" on every update, producing
	// spurious plan churn for SAs (which have no email).
	cfgRenamed := cfg
	cfgRenamed.Name = new("Service Account v2")

	// Flipping `is_service_account` explicitly from true to false is the
	// supported way to "convert" an SA into a regular user; it is immutable
	// server-side, so RequiresReplaceIfConfigured triggers a replacement when
	// the value is set in config.
	cfgRegular := cfgRenamed
	cfgRegular.IsServiceAccount = new(false)
	cfgRegular.Email = new("service-account@coder.com")
	cfgRegular.LoginType = new("password")
	cfgRegular.Password = new("SomeSecurePassword!")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: cfg.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "service-account"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Service Account"),
					resource.TestCheckResourceAttr("coderd_user.test", "is_service_account", "true"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "none"),
					// Service accounts have no email.
					resource.TestCheckResourceAttr("coderd_user.test", "email", ""),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.#", "1"),
					resource.TestCheckTypeSetElemAttr("coderd_user.test", "roles.*", "template-admin"),
				),
			},
			// Import by ID
			{
				ResourceName:            "coderd_user.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"roles"},
			},
			// No-op after import: Coder returns an empty email for service accounts,
			// while config omits email. Optional+Computed plus UseStateForUnknown
			// must make that clean instead of producing a null-vs-empty-string diff.
			{
				Config: cfg.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "service-account"),
					resource.TestCheckResourceAttr("coderd_user.test", "is_service_account", "true"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", ""),
				),
			},
			// Update-only step: changing `name` must not mark `email` as
			// "(known after apply)" — the email attribute's UseStateForUnknown
			// modifier preserves the prior state value across unrelated updates.
			{
				Config: cfgRenamed.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_user.test", plancheck.ResourceActionUpdate),
						// Without UseStateForUnknown on `email`, this would be
						// unknown and ExpectKnownValue would fail.
						plancheck.ExpectKnownValue("coderd_user.test", tfjsonpath.New("email"), knownvalue.StringExact("")),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Service Account v2"),
					resource.TestCheckResourceAttr("coderd_user.test", "is_service_account", "true"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", ""),
				),
			},
			// Explicitly flipping is_service_account from true to false is the
			// supported conversion path and must force replacement.
			{
				Config: cfgRegular.String(t),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("coderd_user.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "is_service_account", "false"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", "service-account@coder.com"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "password"),
				),
			},
		},
	})
}

// TestAccUserResourceValidateConfig exercises the plan-time validation around
// the now-optional email and the service-account constraints. These are config
// errors caught before any API call, so an unlicensed deployment is sufficient.
func TestAccUserResourceValidateConfig(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "user_validate_acc")

	base := testAccUserResourceConfig{
		URL:   client.URL.String(),
		Token: client.SessionToken(),
	}

	// Regular user (not a service account) must still provide an email, even
	// though the attribute is now Optional in the schema.
	noEmail := base
	noEmail.Username = new("no-email")

	// Service accounts must not carry an email, password, or non-none login_type.
	saWithEmail := base
	saWithEmail.Username = new("sa-email")
	saWithEmail.IsServiceAccount = new(true)
	saWithEmail.Email = new("sa@coder.com")

	saWithPassword := base
	saWithPassword.Username = new("sa-password")
	saWithPassword.IsServiceAccount = new(true)
	saWithPassword.Password = new("SomeSecurePassword!")

	saWithEmptyPassword := base
	saWithEmptyPassword.Username = new("sa-empty-password")
	saWithEmptyPassword.IsServiceAccount = new(true)
	saWithEmptyPassword.Password = new("")

	saWithLoginType := base
	saWithLoginType.Username = new("sa-login")
	saWithLoginType.IsServiceAccount = new(true)
	saWithLoginType.LoginType = new("password")

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      noEmail.String(t),
				ExpectError: regexp.MustCompile(`email.+is required`),
			},
			{
				Config:      saWithEmail.String(t),
				ExpectError: regexp.MustCompile(`email.+must not be set`),
			},
			{
				Config:      saWithPassword.String(t),
				ExpectError: regexp.MustCompile(`password.+must not be set`),
			},
			{
				Config:      saWithEmptyPassword.String(t),
				ExpectError: regexp.MustCompile(`password.+must not be set`),
			},
			{
				Config:      saWithLoginType.String(t),
				ExpectError: regexp.MustCompile(`login_type.+must be`),
			},
		},
	})
}

type testAccUserResourceConfig struct {
	URL   string
	Token string

	Username         *string
	Name             *string
	Email            *string
	Roles            *[]string
	LoginType        *string
	Password         *string
	Suspended        *bool
	IsServiceAccount *bool
}

func (c testAccUserResourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

resource "coderd_user" "test" {
	username   = {{orNull .Username}}
	name       = {{orNull .Name}}
	email      = {{orNull .Email}}
	roles      = {{orNull .Roles}}
	login_type = {{orNull .LoginType}}
	password   = {{orNull .Password}}
	suspended  = {{orNull .Suspended}}
	is_service_account = {{orNull .IsServiceAccount}}
}
`
	// Define template functions
	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("test").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)

	return buf.String()
}
