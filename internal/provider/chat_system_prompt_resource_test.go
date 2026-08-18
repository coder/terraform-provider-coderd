package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/stretchr/testify/require"
)

// chatSystemPromptPath is the experimental endpoint backing the resource.
const chatSystemPromptPath = "/api/experimental/chats/config/system-prompt"

const chatSystemPromptResourceAddr = "coderd_chat_system_prompt.test"

// fakeChatCoderd is a minimal stand-in for a Coder deployment, serving just
// the endpoints the provider touches: the two Configure() calls plus the chat
// system prompt singleton.
//
// A fake rather than `integration.StartCoder` for the same reason as the
// OAuth2 settings tests: most of the matrix is about what the *provider* does
// with a given API response, including that the server-side sanitization of
// the stored prompt does not surface as drift. The fake sanitizes on PUT
// exactly like coderd does, which is the behavior under test.
type fakeChatCoderd struct {
	*httptest.Server

	mu            sync.Mutex
	prompt        string
	includeDflt   bool
	requests      []fakeRequest
	putPrompts    []string
	getStatus     int
	putStatus     int
	sanitizeOnPut bool
}

func newFakeChatCoderd(t *testing.T) *fakeChatCoderd {
	t.Helper()

	f := &fakeChatCoderd{includeDflt: true, sanitizeOnPut: true}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeChatCoderd) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, fakeRequest{Method: r.Method, Path: r.URL.Path})
	f.mu.Unlock()

	switch {
	case r.URL.Path == "/api/v2/users/me":
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               "00000000-0000-0000-0000-000000000001",
			"username":         "admin",
			"organization_ids": []string{"00000000-0000-0000-0000-000000000002"},
		})
	case r.URL.Path == "/api/v2/entitlements":
		writeJSON(w, http.StatusOK, codersdk.Entitlements{
			Features: map[codersdk.FeatureName]codersdk.Feature{},
		})
	case r.URL.Path == chatSystemPromptPath && r.Method == http.MethodGet:
		f.mu.Lock()
		status, prompt, include := f.getStatus, f.prompt, f.includeDflt
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		writeJSON(w, http.StatusOK, codersdk.ChatSystemPromptResponse{
			SystemPrompt:               prompt,
			IncludeDefaultSystemPrompt: include,
			DefaultSystemPrompt:        "built-in prompt",
		})
	case r.URL.Path == chatSystemPromptPath && r.Method == http.MethodPut:
		f.mu.Lock()
		status := f.putStatus
		f.mu.Unlock()
		if status != 0 {
			writeJSON(w, status, codersdk.Response{Message: errorMessage(status, "")})
			return
		}
		var req codersdk.UpdateChatSystemPromptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, codersdk.Response{Message: "Bad Request."})
			return
		}
		f.mu.Lock()
		f.putPrompts = append(f.putPrompts, req.SystemPrompt)
		f.prompt = req.SystemPrompt
		if f.sanitizeOnPut {
			// Match coderd: the stored value is the sanitized value.
			f.prompt = sanitizePromptText(req.SystemPrompt)
		}
		if req.IncludeDefaultSystemPrompt != nil {
			f.includeDflt = *req.IncludeDefaultSystemPrompt
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
	}
}

func chatSystemPromptConfig(url, prompt string, includeDefault *bool) string {
	include := ""
	if includeDefault != nil {
		include = fmt.Sprintf("\n\tinclude_default_system_prompt = %t", *includeDefault)
	}
	return oauth2SettingsProviderBlock(url) + fmt.Sprintf(`
resource "coderd_chat_system_prompt" "test" {
	system_prompt = %q%s
}
`, prompt, include)
}

// TestSanitizePromptText pins the local port of coderd's sanitizer to the
// upstream behavior it must mirror for semantic equality to be correct.
func TestSanitizePromptText(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"trailing newline", "prompt\n", "prompt"},
		{"crlf", "a\r\nb\rc", "a\nb\nc"},
		{"zero-width space", "a\u200bb", "ab"},
		{"zwj stripped", "a\u200db", "ab"},
		{"zwnj preserved", "a\u200cb", "a\u200cb"},
		{"bom", "\ufeffprompt", "prompt"},
		{"collapse blank lines", "a\n\n\n\nb", "a\n\nb"},
		{"trailing line whitespace", "a  \nb", "a\nb"},
		{"leading indentation preserved", "a\n  b", "a\n  b"},
		{"idempotent", "  a\u200b\n\n\n\nb  \n", "a\n\nb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizePromptText(tc.in)
			require.Equal(t, tc.want, got)
			// Sanitization must be idempotent: the server stores the
			// sanitized form, and Read compares it against the config's
			// sanitized form.
			require.Equal(t, got, sanitizePromptText(got))
		})
	}
}

// TestChatSystemPromptSemanticEquals covers the custom type directly: two
// prompts are the same setting iff they sanitize to the same string.
func TestChatSystemPromptSemanticEquals(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	for _, tc := range []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "prompt", "prompt", true},
		{"trailing newline", "prompt\n", "prompt", true},
		{"crlf vs lf", "a\r\nb", "a\nb", true},
		{"different text", "prompt a", "prompt b", false},
		{"whitespace-only difference inside a line", "a b", "a  b", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, diags := newChatSystemPromptTextValue(tc.a).StringSemanticEquals(ctx, newChatSystemPromptTextValue(tc.b))
			require.False(t, diags.HasError())
			require.Equal(t, tc.want, got)
		})
	}
}

func TestChatSystemPromptLengthValidator(t *testing.T) {
	t.Parallel()

	f := newFakeChatCoderd(t)

	// The sanitized form is what the server measures, so padding that
	// sanitizes away must not trip the validator.
	okPrompt := strings.Repeat("a", maxChatSystemPromptBytes) + "\n\n\n"
	tooLong := strings.Repeat("a", maxChatSystemPromptBytes+1)

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      chatSystemPromptConfig(f.URL, tooLong, nil),
				ExpectError: regexp.MustCompile("System Prompt Too Long"),
			},
			{
				Config: chatSystemPromptConfig(f.URL, okPrompt, nil),
			},
		},
	})
}

// TestAccChatSystemPromptResource exercises the full lifecycle against the
// fake: create, refresh without drift despite server-side sanitization,
// update, and destroy resetting the deployment defaults.
func TestAccChatSystemPromptResource(t *testing.T) {
	t.Parallel()

	f := newFakeChatCoderd(t)
	includeFalse := false

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with a trailing newline, the `file()` everyday case. The
			// fake stores the sanitized (trimmed) form, like coderd does.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a helpful agent.\n", nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						chatSystemPromptResourceAddr,
						tfjsonpath.New("include_default_system_prompt"),
						knownvalue.Bool(true),
					),
				},
			},
			// Re-planning the same config must be empty: the live value is
			// the sanitized form, which is semantically equal.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a helpful agent.\n", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
			// A real edit must show up and apply.
			{
				Config: chatSystemPromptConfig(f.URL, "You are a very helpful agent.\n", &includeFalse),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						chatSystemPromptResourceAddr,
						tfjsonpath.New("include_default_system_prompt"),
						knownvalue.Bool(false),
					),
				},
			},
		},
	})

	// Destroy (run by resource.Test after the last step) resets the
	// deployment defaults rather than stranding the last-applied value.
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Empty(t, f.prompt)
	require.True(t, f.includeDflt)
}

// TestAccChatSystemPromptImport adopts a live out-of-band prompt without
// issuing any PUT.
func TestAccChatSystemPromptImport(t *testing.T) {
	t.Parallel()

	f := newFakeChatCoderd(t)
	f.mu.Lock()
	f.prompt = "configured in the dashboard"
	f.mu.Unlock()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             chatSystemPromptConfig(f.URL, "configured in the dashboard", nil),
				ResourceName:       chatSystemPromptResourceAddr,
				ImportState:        true,
				ImportStatePersist: true,
				// The ID is required by the CLI syntax but unused.
				ImportStateId: "chat_system_prompt",
			},
			// After import, the matching config plans clean: nothing to
			// overwrite, nothing to PUT.
			{
				Config: chatSystemPromptConfig(f.URL, "configured in the dashboard", nil),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectEmptyPlan(),
					},
				},
			},
		},
	})

	// Adopting an existing value must never write it back. The only PUT in
	// the whole test is the framework's final `terraform destroy`, which
	// resets the prompt to empty.
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(t, []string{""}, f.putPrompts)
}

// TestAccChatSystemPromptUnsupportedVersion pins the 404-to-version-hint
// mapping: an old deployment produces one actionable error.
func TestAccChatSystemPromptUnsupportedVersion(t *testing.T) {
	t.Parallel()

	f := newFakeChatCoderd(t)
	f.mu.Lock()
	f.getStatus = http.StatusNotFound
	f.putStatus = http.StatusNotFound
	f.mu.Unlock()

	resource.Test(t, resource.TestCase{
		IsUnitTest:               true,
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: chatSystemPromptConfig(f.URL, "prompt", nil),
				// Terraform wraps error text, so match the summary line only.
				ExpectError: regexp.MustCompile("Unsupported Coder Version"),
			},
		},
	})
}
