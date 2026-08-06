package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserDataSourceLookupUserByEmailFallback(t *testing.T) {
	t.Parallel()

	userID := uuid.New()
	orgID := uuid.New()
	email := "example@coder.com"
	var exactCalls, fallbackCalls int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/users", r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("q") {
		case "email:" + email:
			exactCalls++
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(codersdk.Response{
				Message: "Invalid user search query.",
				Validations: []codersdk.ValidationError{
					{Field: "email", Detail: `"email" is not a valid query param`},
				},
			})
		case email:
			fallbackCalls++
			assert.Equal(t, "100", r.URL.Query().Get("limit"))
			_ = json.NewEncoder(w).Encode(codersdk.GetUsersResponse{
				Users: []codersdk.User{
					{
						ReducedUser: codersdk.ReducedUser{
							MinimalUser: codersdk.MinimalUser{
								ID:        userID,
								Username:  "example",
								Name:      "Example User",
								AvatarURL: "",
							},
							Email:           email,
							CreatedAt:       time.Now(),
							LastSeenAt:      time.Now(),
							Status:          codersdk.UserStatusActive,
							LoginType:       codersdk.LoginTypePassword,
							UpdatedAt:       time.Now(),
							ThemePreference: "",
						},
						OrganizationIDs: []uuid.UUID{orgID},
						Roles:           []codersdk.SlimRole{{Name: "auditor"}},
					},
				},
				Count: 1,
			})
		default:
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
	}))
	defer srv.Close()

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	client := codersdk.New(clientURL)
	user, err := lookupUserByEmail(t.Context(), client, email)
	require.NoError(t, err)
	assert.Equal(t, userID, user.ID)
	assert.Equal(t, email, user.Email)
	assert.Equal(t, 1, exactCalls)
	assert.Equal(t, 1, fallbackCalls)
}

func TestAccUserDataSource(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "user_data_acc")
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	user, err := client.CreateUser(ctx, codersdk.CreateUserRequest{
		Email:          "example@coder.com",
		Username:       "example",
		Password:       "SomeSecurePassword!",
		UserLoginType:  "password",
		OrganizationID: firstUser.OrganizationIDs[0],
	})
	require.NoError(t, err)
	_, err = client.UpdateUserRoles(ctx, user.Username, codersdk.UpdateRoles{
		Roles: []string{"auditor"},
	})
	require.NoError(t, err)
	_, err = client.UpdateUserProfile(ctx, user.Username, codersdk.UpdateUserProfileRequest{
		Username: user.Username,
		Name:     "Example User",
	})
	require.NoError(t, err)

	checkFn := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("data.coderd_user.test", "username", "example"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "name", "Example User"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "email", "example@coder.com"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "roles.#", "1"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "roles.0", "auditor"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "login_type", "password"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "suspended", "false"),
		resource.TestCheckResourceAttr("data.coderd_user.test", "is_service_account", "false"),
	)
	t.Run("UserByUsernameOk", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:      client.URL.String(),
			Token:    client.SessionToken(),
			Username: ptr.Ref(user.Username),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  checkFn,
				},
			},
		})
	})

	t.Run("UserByIDOk", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    ptr.Ref(user.ID.String()),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// User by ID
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  checkFn,
				},
			},
		})
	})
	t.Run("UserByEmailOk", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Email: ptr.Ref(user.Email),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: cfg.String(t),
					Check:  checkFn,
				},
			},
		})
	})
	t.Run("NeitherIDNorUsernameError", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			// Neither ID nor Username
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`At least one of these attributes must be configured: \[id,username,email\]`),
				},
			},
		})
	})

	t.Run("InvalidUUIDError", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			ID:    ptr.Ref("invalid-uuid"),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`The provided value cannot be parsed as a UUID`),
				},
			},
		})
	})
}

// TestAccUserDataSourceServiceAccount verifies the data source surfaces
// is_service_account==true and an empty email for a service account. Service
// accounts are a Premium feature, so a licensed deployment is required.
func TestAccUserDataSourceServiceAccount(t *testing.T) {
	t.Parallel()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("Acceptance tests are disabled.")
	}
	ctx := t.Context()
	client := integration.StartCoder(ctx, t, "user_data_service_account_acc", integration.UseLicense)
	firstUser, err := client.User(ctx, codersdk.Me)
	require.NoError(t, err)
	user, err := client.CreateUserWithOrgs(ctx, codersdk.CreateUserRequestWithOrgs{
		Username:        "service-account",
		Name:            "Service Account",
		UserLoginType:   "none",
		OrganizationIDs: []uuid.UUID{firstUser.OrganizationIDs[0]},
		ServiceAccount:  true,
	})
	require.NoError(t, err)

	cfg := testAccUserDataSourceConfig{
		URL:      client.URL.String(),
		Token:    client.SessionToken(),
		Username: ptr.Ref(user.Username),
	}
	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg.String(t),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.coderd_user.test", "username", "service-account"),
					resource.TestCheckResourceAttr("data.coderd_user.test", "is_service_account", "true"),
					// Service accounts have no email.
					resource.TestCheckResourceAttr("data.coderd_user.test", "email", ""),
					resource.TestCheckResourceAttr("data.coderd_user.test", "login_type", "none"),
				),
			},
		},
	})
}

type testAccUserDataSourceConfig struct {
	URL   string
	Token string

	ID       *string
	Username *string
	Email    *string
}

func (c testAccUserDataSourceConfig) String(t *testing.T) string {
	t.Helper()
	tpl := `
provider coderd {
	url = "{{.URL}}"
	token = "{{.Token}}"
}

data "coderd_user" "test" {
	id       = {{orNull .ID}}
	username = {{orNull .Username}}
	email    = {{orNull .Email}}
}`

	funcMap := template.FuncMap{
		"orNull": PrintOrNull,
	}

	buf := strings.Builder{}
	tmpl, err := template.New("userDataSource").Funcs(funcMap).Parse(tpl)
	require.NoError(t, err)

	err = tmpl.Execute(&buf, c)
	require.NoError(t, err)
	return buf.String()
}
