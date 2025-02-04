package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"cdr.dev/slog"
	"github.com/google/uuid"
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

type CoderdProviderData struct {
	Client                *codersdk.Client
	DefaultOrganizationID uuid.UUID
	Features              map[codersdk.FeatureName]codersdk.Feature
}

// CoderdProviderModel describes the provider data model.
type CoderdProviderModel struct {
	URL   types.String `tfsdk:"url"`
	Token types.String `tfsdk:"token"`

	DefaultOrganizationID UUID `tfsdk:"default_organization_id"`
}

func (p *CoderdProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "coderd"
	resp.Version = p.version
}

func (p *CoderdProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
The coderd provider can be used to manage resources on a Coder deployment. The provider exposes resources and data sources for users, groups, templates, and workspace proxies.

~> **Warning**
This provider is only compatible with Coder version [2.10.1](https://github.com/coder/coder/releases/tag/v2.10.1) and later.
`,
		Attributes: map[string]schema.Attribute{
			"url": schema.StringAttribute{
				MarkdownDescription: "URL to the Coder deployment. Defaults to `$CODER_URL`.",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "API token for communicating with the deployment. Most resource types require elevated permissions. Defaults to `$CODER_SESSION_TOKEN`.",
				Optional:            true,
			},
			"default_organization_id": schema.StringAttribute{
				MarkdownDescription: "Default organization ID to use when creating resources. Defaults to the first organization the token has access to.",
				CustomType:          UUIDType,
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

	rawURL := data.URL.ValueString()
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		scheme := "https"
		if strings.HasPrefix(rawURL, "localhost") {
			scheme = "http"
		}
		rawURL = fmt.Sprintf("%s://%s", scheme, rawURL)
	}

	url, err := url.Parse(rawURL)
	if err != nil {
		resp.Diagnostics.AddError("url", "url is not a valid URL: "+err.Error())
		return
	}
	client := codersdk.New(url)
	client.SetLogger(slog.Make(tfslog{}).Leveled(slog.LevelDebug))
	client.SetSessionToken(data.Token.ValueString())
	if data.DefaultOrganizationID.IsNull() {
		user, err := client.User(ctx, codersdk.Me)
		if err != nil {
			resp.Diagnostics.AddError("default_organization_id", "failed to get default organization ID: "+err.Error())
			return
		}
		data.DefaultOrganizationID = UUIDValue(user.OrganizationIDs[0])
	}
	entitlements, err := client.Entitlements(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "failed to get deployment entitlements: "+err.Error())
	}

	providerData := &CoderdProviderData{
		Client:                client,
		DefaultOrganizationID: data.DefaultOrganizationID.ValueUUID(),
		Features:              entitlements.Features,
	}
	resp.DataSourceData = providerData
	resp.ResourceData = providerData
}

func (p *CoderdProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUserResource,
		NewGroupResource,
		NewTemplateResource,
		NewWorkspaceProxyResource,
		NewLicenseResource,
		NewOrganizationResource,
		NewProvisionerKeyResource,
		NewOrganizationSyncSettingsResource,
	}
}

func (p *CoderdProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewGroupDataSource,
		NewUserDataSource,
		NewOrganizationDataSource,
		NewTemplateDataSource,
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
