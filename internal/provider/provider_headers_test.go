package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestConfigType() tftypes.Object {
	return tftypes.Object{
		AttributeTypes: map[string]tftypes.Type{
			"url":                     tftypes.String,
			"token":                   tftypes.String,
			"default_organization_id": tftypes.String,
			"headers": tftypes.Map{
				ElementType: tftypes.String,
			},
		},
	}
}

func newMockServer(headersOut *atomic.Value) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if headersOut != nil {
			headersOut.Store(r.Header.Clone())
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000001","username":"admin","organization_ids":["00000000-0000-0000-0000-000000000002"]}`))
	}))
}

func configureProvider(t *testing.T, cfgVal tftypes.Value) *provider.ConfigureResponse {
	t.Helper()
	p := &CoderdProvider{version: "test"}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	require.Empty(t, schemaResp.Diagnostics)

	configResp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: tfsdkConfig(t, schemaResp.Schema, cfgVal),
	}, configResp)
	return configResp
}

func tfsdkConfig(t *testing.T, s schema.Schema, val tftypes.Value) tfsdk.Config {
	t.Helper()
	return tfsdk.Config{
		Schema: s,
		Raw:    val,
	}
}

func TestProviderHeaders_HCLConfig(t *testing.T) {
	t.Parallel()

	var received atomic.Value
	srv := newMockServer(&received)
	defer srv.Close()

	cfgVal := tftypes.NewValue(newTestConfigType(), map[string]tftypes.Value{
		"url":                     tftypes.NewValue(tftypes.String, srv.URL),
		"token":                   tftypes.NewValue(tftypes.String, "test-token"),
		"default_organization_id": tftypes.NewValue(tftypes.String, nil),
		"headers": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
			"X-Custom-Header":          tftypes.NewValue(tftypes.String, "custom-value"),
			"X-Coder-Bypass-Ratelimit": tftypes.NewValue(tftypes.String, "true"),
		}),
	})

	configureProvider(t, cfgVal)

	require.NotNil(t, received.Load(), "expected at least one HTTP request")
	headers := received.Load().(http.Header)
	assert.Equal(t, "custom-value", headers.Get("X-Custom-Header"))
	assert.Equal(t, "true", headers.Get("X-Coder-Bypass-Ratelimit"))
}

func TestProviderHeaders_EnvVar(t *testing.T) {

	var received atomic.Value
	srv := newMockServer(&received)
	defer srv.Close()

	t.Setenv("CODER_HEADER", "X-Env-Header=env-value,X-Another=second-value")

	cfgVal := tftypes.NewValue(newTestConfigType(), map[string]tftypes.Value{
		"url":                     tftypes.NewValue(tftypes.String, srv.URL),
		"token":                   tftypes.NewValue(tftypes.String, "test-token"),
		"default_organization_id": tftypes.NewValue(tftypes.String, nil),
		"headers":                 tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
	})

	configureProvider(t, cfgVal)

	require.NotNil(t, received.Load(), "expected at least one HTTP request")
	headers := received.Load().(http.Header)
	assert.Equal(t, "env-value", headers.Get("X-Env-Header"))
	assert.Equal(t, "second-value", headers.Get("X-Another"))
}

func TestProviderHeaders_HCLOverridesEnv(t *testing.T) {

	var received atomic.Value
	srv := newMockServer(&received)
	defer srv.Close()

	t.Setenv("CODER_HEADER", "X-Env-Header=should-not-appear")

	cfgVal := tftypes.NewValue(newTestConfigType(), map[string]tftypes.Value{
		"url":                     tftypes.NewValue(tftypes.String, srv.URL),
		"token":                   tftypes.NewValue(tftypes.String, "test-token"),
		"default_organization_id": tftypes.NewValue(tftypes.String, nil),
		"headers": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{
			"X-HCL-Header": tftypes.NewValue(tftypes.String, "hcl-wins"),
		}),
	})

	configureProvider(t, cfgVal)

	require.NotNil(t, received.Load(), "expected at least one HTTP request")
	headers := received.Load().(http.Header)
	assert.Equal(t, "hcl-wins", headers.Get("X-HCL-Header"))
	assert.Empty(t, headers.Get("X-Env-Header"), "env var headers should not appear when HCL headers are set")
}

func TestProviderHeaders_InvalidEnvVar(t *testing.T) {

	srv := newMockServer(nil)
	defer srv.Close()

	t.Setenv("CODER_HEADER", "malformed-no-equals")

	cfgVal := tftypes.NewValue(newTestConfigType(), map[string]tftypes.Value{
		"url":                     tftypes.NewValue(tftypes.String, srv.URL),
		"token":                   tftypes.NewValue(tftypes.String, "test-token"),
		"default_organization_id": tftypes.NewValue(tftypes.String, nil),
		"headers":                 tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
	})

	configResp := configureProvider(t, cfgVal)

	require.True(t, configResp.Diagnostics.HasError(), "expected an error for malformed CODER_HEADERS")
	found := false
	for _, d := range configResp.Diagnostics.Errors() {
		if d.Summary() == "headers" {
			found = true
			break
		}
	}
	require.True(t, found, "expected error with summary 'headers'")
}

func TestProviderHeaders_NoHeaders(t *testing.T) {
	t.Parallel()

	var received atomic.Value
	srv := newMockServer(&received)
	defer srv.Close()

	cfgVal := tftypes.NewValue(newTestConfigType(), map[string]tftypes.Value{
		"url":                     tftypes.NewValue(tftypes.String, srv.URL),
		"token":                   tftypes.NewValue(tftypes.String, "test-token"),
		"default_organization_id": tftypes.NewValue(tftypes.String, nil),
		"headers":                 tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, nil),
	})

	configureProvider(t, cfgVal)

	require.NotNil(t, received.Load(), "expected at least one HTTP request")
	headers := received.Load().(http.Header)
	assert.Empty(t, headers.Get("X-Custom-Header"))
	assert.Empty(t, headers.Get("X-Coder-Bypass-Ratelimit"))
}
