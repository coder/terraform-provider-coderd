// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/url"
	"os"
	"strings"

	"cdr.dev/slog"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/coder/coder/v2/codersdk"
)

// Ensure CoderdProvider satisfies various provider interfaces.
var _ provider.Provider = &CoderdProvider{}
var _ provider.ProviderWithFunctions = &CoderdProvider{}

// CoderdProvider defines the provider implementation.
type CoderdProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// CoderdProviderModel describes the provider data model.
type CoderdProviderModel struct {
	URL   types.String `tfsdk:"url"`
	Token types.String `tfsdk:"token"`
}

func (p *CoderdProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coderd"
	resp.Version = p.version
}

func (p *CoderdProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				MarkdownDescription: "URL to the Coder deployment. Defaults to $CODER_URL.",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "API token for communicating with the deployment. Most resource types require elevated permissions. Defaults to $CODER_SESSION_TOKEN.",
				Optional:            true,
			},
		},
	}
}

func (p *CoderdProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data CoderdProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	if data.URL.ValueString() == "" {
		urlEnv, ok := os.LookupEnv("CODER_URL")
		if !ok {
			resp.Diagnostics.AddError("url", "url or $CODER_URL is required")
			return
		}
		data.URL = types.StringValue(urlEnv)
	}
	if data.Token.ValueString() == "" {
		tokenEnv, ok := os.LookupEnv("CODER_SESSION_TOKEN")
		if !ok {
			resp.Diagnostics.AddError("token", "token or $CODER_SESSION_TOKEN is required")
			return
		}
		data.Token = types.StringValue(tokenEnv)
	}

	url, err := url.Parse(data.URL.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("url", "url is not a valid URL: "+err.Error())
		return
	}
	client := codersdk.New(url)
	client.SetLogger(slog.Make(tfslog{}).Leveled(slog.LevelDebug))
	client.SetSessionToken(data.Token.ValueString())
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *CoderdProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUserResource,
	}
}

func (p *CoderdProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewExampleDataSource,
	}
}

func (p *CoderdProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &CoderdProvider{
			version: version,
		}
	}
}

// tfslog redirects slog entries to tflog.
type tfslog struct{}

var _ slog.Sink = tfslog{}

// LogEntry implements slog.Sink.
func (t tfslog) LogEntry(ctx context.Context, e slog.SinkEntry) {
	m := map[string]any{
		"time": e.Time.Unix(),
		"func": e.Func,
		"file": e.File,
		"line": e.Line,
	}
	for _, f := range e.Fields {
		m[f.Name] = f.Value
	}

	msg := e.Message
	if len(e.LoggerNames) > 0 {
		msg = "[" + strings.Join(e.LoggerNames, ".") + "] " + msg
	}

	switch e.Level {
	case slog.LevelDebug:
		tflog.Debug(ctx, msg, m)
	case slog.LevelInfo:
		tflog.Info(ctx, msg, m)
	case slog.LevelWarn:
		tflog.Warn(ctx, msg, m)
	case slog.LevelError, slog.LevelFatal:
		tflog.Error(ctx, msg, m)
	}
}

// Sync implements slog.Sink.
func (t tfslog) Sync() {}
