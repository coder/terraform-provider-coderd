package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/coder/coder/v2/coderd/util/ptr"
	"github.com/coder/coder/v2/codersdk"
	"github.com/coder/terraform-provider-coderd/integration"
	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserDataSourceLookupUserByEmailPaginates(t *testing.T) {
	t.Parallel()

	email := "example@coder.com"
	pageOne := make([]codersdk.User, userEmailLookupPageLimit)
	for i := range pageOne {
		pageOne[i] = codersdk.User{
			ReducedUser: codersdk.ReducedUser{
				MinimalUser: codersdk.MinimalUser{ID: uuid.New(), Username: fmt.Sprintf("user-%03d", i)},
				Email:       fmt.Sprintf("user-%03d@coder.com", i),
			},
		}
	}
	lastID := pageOne[len(pageOne)-1].ID
	targetID := uuid.New()
	target := codersdk.User{
		ReducedUser: codersdk.ReducedUser{
			MinimalUser: codersdk.MinimalUser{ID: targetID, Username: "example"},
			Email:       "Example@Coder.com",
		},
	}
	var requests []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		response := codersdk.GetUsersResponse{Users: pageOne, Count: len(pageOne) + 1}
		if r.URL.Query().Get("after_id") == lastID.String() {
			response = codersdk.GetUsersResponse{Users: []codersdk.User{target}, Count: 1}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	user, err := lookupUserByEmail(t.Context(), codersdk.New(clientURL), email)
	require.NoError(t, err)
	assert.Equal(t, targetID, user.ID)
	require.Len(t, requests, 2)
	assert.Equal(t, email, requests[0].Get("q"))
	assert.Equal(t, "100", requests[0].Get("limit"))
	assert.Empty(t, requests[0].Get("after_id"))
	assert.Equal(t, lastID.String(), requests[1].Get("after_id"))
}

func TestUserDataSourceLookupUserByEmailNotFound(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(codersdk.GetUsersResponse{})
	}))
	defer srv.Close()

	clientURL, err := url.Parse(srv.URL)
	require.NoError(t, err)
	_, err = lookupUserByEmail(t.Context(), codersdk.New(clientURL), "missing@coder.com")
	require.ErrorIs(t, err, errUserNotFound)
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
	t.Run("UserByEmailNotFound", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
			Email: ptr.Ref("missing@coder.com"),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config:      cfg.String(t),
					ExpectError: regexp.MustCompile(`User Not Found`),
				},
			},
		})
	})

	t.Run("NoIdentifierError", func(t *testing.T) {
		cfg := testAccUserDataSourceConfig{
			URL:   client.URL.String(),
			Token: client.SessionToken(),
		}
		resource.Test(t, resource.TestCase{
			IsUnitTest:               true,
			PreCheck:                 func() { testAccPreCheck(t) },
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
