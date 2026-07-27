package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/coder/coder/v2/codersdk"
)

// oauth2SettingsPath is the endpoint added by coder/coder#27316.
const oauth2SettingsPath = "/api/v2/oauth2-provider/settings"

// fakeCoderd is a minimal stand-in for a Coder deployment, serving just the
// endpoints the provider touches: the two `Configure()` calls plus the OAuth2
// provider settings singleton.
//
// A fake rather than `integration.StartCoder` because most of the OAuth2
// settings test matrix is about what the *provider* does with a given API
// response: that a 404 becomes a version hint, that a 403 becomes a clean
// diagnostic, that import issues no PUT at all. Those are assertions about
// requests made and not made, which a real deployment cannot report.
type fakeCoderd struct {
	*httptest.Server

	mu         sync.Mutex
	dcrEnabled bool
	requests   []fakeRequest

	// getStatus and putStatus, when non-zero, make the settings endpoint fail
	// with that status instead of behaving normally. Used to model an old
	// deployment (404), an unauthorized token (403), and a transient outage
	// (500). The provider cannot distinguish "the token lacks the role" from
	// "the API said 403", which is exactly the contract under test: the RBAC
	// decision itself belongs to coderd, not here.
	getStatus int
	putStatus int

	// errMessage, when non-empty, replaces the generic message statusMessage
	// derives from the status code. Needed because coderd reuses 403 for two
	// unrelated refusals -- an RBAC denial and the `oauth2` experiment gate --
	// and only the message distinguishes them.
	errMessage string

	// beforeGet runs before each settings GET is answered, and beforeSettingsPut
	// before each PUT. Both are used to simulate another actor mutating the
	// deployment mid-apply.
	beforeGet func(f *fakeCoderd)
	beforePut func(f *fakeCoderd)
}

type fakeRequest struct {
	Method string
	Path   string
	// DCREnabled is the body value on a settings PUT, valid only when DCRSent
	// is true.
	DCREnabled bool
	// DCRSent records whether the PUT body carried the field at all. The API
	// reads an omitted field as "leave the current value alone", so "sent
	// false" and "omitted" are different requests with different effects.
	DCRSent bool
}

func newFakeCoderd(t *testing.T) *fakeCoderd {
	t.Helper()

	f := &fakeCoderd{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeCoderd) handle(w http.ResponseWriter, r *http.Request) {
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
	case r.URL.Path == oauth2SettingsPath && r.Method == http.MethodGet:
		f.handleSettingsGet(w)
	case r.URL.Path == oauth2SettingsPath && r.Method == http.MethodPut:
		f.handleSettingsPut(w, r)
	default:
		writeJSON(w, http.StatusNotFound, codersdk.Response{Message: "Not Found."})
	}
}

func (f *fakeCoderd) handleSettingsGet(w http.ResponseWriter) {
	f.mu.Lock()
	hook := f.beforeGet
	f.mu.Unlock()
	if hook != nil {
		hook(f)
	}

	f.mu.Lock()
	status, enabled, msg := f.getStatus, f.dcrEnabled, f.errMessage
	f.mu.Unlock()

	if status != 0 {
		writeJSON(w, status, codersdk.Response{Message: errorMessage(status, msg)})
		return
	}
	// A GET always answers with a non-nil pointer, matching the documented
	// contract on codersdk.OAuth2ProviderSettings.
	writeJSON(w, http.StatusOK, codersdk.OAuth2ProviderSettings{DynamicClientRegistrationEnabled: &enabled})
}

func (f *fakeCoderd) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hook := f.beforePut
	status := f.putStatus
	msg := f.errMessage
	f.mu.Unlock()
	if hook != nil {
		hook(f)
	}

	var body codersdk.OAuth2ProviderSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, codersdk.Response{Message: "Malformed body.", Detail: err.Error()})
		return
	}

	f.mu.Lock()
	// Record what was actually sent, so assertions can check both the value
	// and whether the field was present at all. A nil field means the caller
	// omitted it, which the real API treats as "leave the current value
	// alone" -- mirrored here so a regression that stops sending the value
	// (making Delete's reset a silent no-op) fails a test instead of passing.
	f.requests[len(f.requests)-1].DCRSent = body.DynamicClientRegistrationEnabled != nil
	if body.DynamicClientRegistrationEnabled != nil {
		f.requests[len(f.requests)-1].DCREnabled = *body.DynamicClientRegistrationEnabled
		if status == 0 {
			f.dcrEnabled = *body.DynamicClientRegistrationEnabled
		}
	}
	resolved := f.dcrEnabled
	f.mu.Unlock()

	if status != 0 {
		writeJSON(w, status, codersdk.Response{Message: errorMessage(status, msg)})
		return
	}
	writeJSON(w, http.StatusOK, codersdk.OAuth2ProviderSettings{DynamicClientRegistrationEnabled: &resolved})
}

// oauth2ExperimentOffMessage is the message coderd's `httpmw.RequireExperiment`
// returns alongside its 403 when the OAuth2 experiment is disabled, copied
// verbatim from the single-experiment branch of that middleware:
//
//	fmt.Sprintf("%s functionality requires enabling the '%s' experiment.",
//	    experiment.DisplayName(), experiment)
//
// Reproduced exactly because isOAuth2ExperimentOff discriminates on this text --
// a paraphrase here would let the test pass while the real message stopped
// matching.
const oauth2ExperimentOffMessage = "OAuth2 Provider Functionality functionality requires enabling the 'oauth2' experiment."

// errorMessage returns the override when set, otherwise the generic message for
// the status code.
func errorMessage(status int, override string) string {
	if override != "" {
		return override
	}
	return statusMessage(status)
}

// statusMessage mirrors the shape of the message coderd itself returns, so the
// provider's error text is assertable.
func statusMessage(status int) string {
	switch status {
	case http.StatusNotFound:
		return "Not Found."
	case http.StatusForbidden:
		return "Forbidden."
	default:
		return fmt.Sprintf("Internal error (%d).", status)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// --- assertion helpers ---

// SetDCREnabled sets the live value, standing in for an out-of-band change via
// the CLI, the deployment settings UI, or another Terraform run.
func (f *fakeCoderd) SetDCREnabled(v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dcrEnabled = v
}

func (f *fakeCoderd) DCREnabled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dcrEnabled
}

func (f *fakeCoderd) SetGetStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getStatus = status
}

func (f *fakeCoderd) SetPutStatus(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putStatus = status
}

// SetExperimentOff makes both settings verbs fail the way a deployment without
// the `oauth2` experiment does: a 403 whose message names the experiment. This
// is the only practical way to reach that path -- `scripts/develop.sh` cannot,
// because development builds bypass the experiment check entirely.
func (f *fakeCoderd) SetExperimentOff() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getStatus = http.StatusForbidden
	f.putStatus = http.StatusForbidden
	f.errMessage = oauth2ExperimentOffMessage
}

func (f *fakeCoderd) SetBeforeGet(hook func(f *fakeCoderd)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeGet = hook
}

// SettingsRequests returns every request made to the settings endpoint, in
// order.
func (f *fakeCoderd) SettingsRequests() []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeRequest
	for _, req := range f.requests {
		if req.Path == oauth2SettingsPath {
			out = append(out, req)
		}
	}
	return out
}

// SettingsRequestCount counts settings requests with the given method.
func (f *fakeCoderd) SettingsRequestCount(method string) int {
	n := 0
	for _, req := range f.SettingsRequests() {
		if req.Method == method {
			n++
		}
	}
	return n
}
