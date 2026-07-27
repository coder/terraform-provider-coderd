package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &OAuth2ProviderSettingsDataSource{}

func NewOAuth2ProviderSettingsDataSource() datasource.DataSource {
	return &OAuth2ProviderSettingsDataSource{}
}

// OAuth2ProviderSettingsDataSource defines the data source implementation.
type OAuth2ProviderSettingsDataSource struct {
	data *CoderdProviderData
}

// OAuth2ProviderSettingsDataSourceModel describes the data source data model.
type OAuth2ProviderSettingsDataSourceModel struct {
	DynamicClientRegistrationEnabled types.Bool `tfsdk:"dynamic_client_registration_enabled"`
}

func (d *OAuth2ProviderSettingsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oauth2_provider_settings"
}

func (d *OAuth2ProviderSettingsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `The deployment-wide OAuth2 provider settings.

Use this data source to read the current settings without taking ownership of
them. Because the settings are a deployment-wide singleton, only one Terraform
configuration can own the ` + "`coderd_oauth2_provider_settings`" + ` *resource*; any other
configuration that needs the value should read it here instead.

-> This data source only issues a ` + "`GET`" + `, so it works with any token holding
read access on the deployment configuration, which is a broader set of roles
than the resource's write path requires. An Auditor token, for example, can
read this data source but cannot own the equivalent resource.

~> **Warning**
This data source is only compatible with Coder version [` + oauth2ProviderSettingsMinVersion + `](https://github.com/coder/coder/releases/tag/v` + oauth2ProviderSettingsMinVersion + `) and later.

~> **Warning**
The deployment must have the ` + "`" + oauth2ProviderSettingsExperiment + "`" + ` experiment enabled (` + "`CODER_EXPERIMENTS=" + oauth2ProviderSettingsExperiment + "`" + ` or ` + "`--experiments=" + oauth2ProviderSettingsExperiment + "`" + `). The experiment gates the entire ` + "`/api/v2/oauth2-provider`" + ` route rather than just its write path, so while it is off this data source cannot read the setting either — unlike an authorization failure, a read-only token is no way around it.
`,

		Attributes: map[string]schema.Attribute{
			"dynamic_client_registration_enabled": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether OAuth2 Dynamic Client Registration ([RFC 7591](https://datatracker.ietf.org/doc/html/rfc7591)) " +
					"is enabled for the deployment. A deployment that has never configured this setting reads back as `false`.",
			},
		},
	}
}

func (d *OAuth2ProviderSettingsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	data, ok := req.ProviderData.(*CoderdProviderData)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.data = data
}

func (d *OAuth2ProviderSettingsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OAuth2ProviderSettingsDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// GET only. This data source must never call PutOAuth2ProviderSettings:
	// that invariant is what makes it safe to declare alongside the resource
	// without risking a write conflict.
	settings, err := d.data.Client.OAuth2ProviderSettings(ctx)
	if err != nil {
		resp.Diagnostics.Append(oauth2ProviderSettingsDiag("read", err)...)
		return
	}

	data.DynamicClientRegistrationEnabled = types.BoolValue(dcrEnabledOrDefault(settings))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
