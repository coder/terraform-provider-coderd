// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

/*
import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccUserResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read testing
			{
				Config: testAccUserResourceConfig{
					Username: "example",
					Name:     "Example User",
					Email:    "example@coder.com",
					Roles:    []string{"owner", "auditor"},
					LoginType: "password",
					Password: "example-password",
					Suspended: true,
				}.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_user.test", "username", "example"),
					resource.TestCheckResourceAttr("coderd_user.test", "name", "Example User"),
					resource.TestCheckResourceAttr("coderd_user.test", "email", "example@coder.com"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.#", "2"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.0", "owner"),
					resource.TestCheckResourceAttr("coderd_user.test", "roles.1", "auditor"),
					resource.TestCheckResourceAttr("coderd_user.test", "login_type", "password"),
					resource.TestCheckResourceAttr("coderd_user.test", "password", "example-password"),
					resource.TestCheckResourceAttr("coderd_user.test", "suspended", "true"),
				),
			},
			// ImportState testing
			{
				ResourceName:      "coderd_user.test",
				ImportState:       true,
				ImportStateVerify: true,
				// This is not normally necessary, but is here because this
				// example code does not have an actual upstream service.
				// Once the Read method is able to refresh information from
				// the upstream service, this can be removed.
				ImportStateVerifyIgnore: []string{"configurable_attribute", "defaulted"},
			},
			// Update and Read testing
			{
				Config: testAccUserResourceConfig{}
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("coderd_example.test", "configurable_attribute", "two"),
				),
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

type testAccUserResourceConfig struct {
	Username  string
	Name      string
	Email     string
	Roles     []string
	LoginType string
	Password  string
	Suspended bool
}

func (c testAccUserResourceConfig) String() string {
	sb := strings.Builder{}
	sb.WriteString(`resource "coderd_user" "test" {` + "\n")
	sb.WriteString(fmt.Sprintf("  username = %q\n", c.Username))
	if c.Name != "" {
		sb.WriteString(fmt.Sprintf("  name = %q\n", c.Name))
	}
	sb.WriteString(fmt.Sprintf("  email = %q\n", c.Email))
	if len(c.Roles) > 0 {
		rolesQuoted := make([]string, len(c.Roles))
		for i, role := range c.Roles {
			rolesQuoted[i] = fmt.Sprintf("%q", role)
		}
		sb.WriteString(fmt.Sprintf("  roles = [%s]\n", strings.Join(rolesQuoted, ", ")))
	}
	if c.LoginType != "" {
		sb.WriteString(fmt.Sprintf("  login_type = %q", c.LoginType))
	}
	if c.Password != "" {
		sb.WriteString(fmt.Sprintf("  password = %q", c.Password))
	}
	if c.Suspended {
		sb.WriteString("  suspended = true\n")
	}
	sb.WriteString(`}`)
	return sb.String()
}
*/