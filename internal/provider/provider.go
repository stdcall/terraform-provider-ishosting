package provider

import (
	"context"
	"os"

	"terraform-provider-ishosting/internal/client"
	"terraform-provider-ishosting/internal/datasources"
	"terraform-provider-ishosting/internal/resources"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &IsHostingProvider{}

type IsHostingProvider struct {
	version string
}

type IsHostingProviderModel struct {
	APIToken types.String `tfsdk:"api_token"`
	BaseURL  types.String `tfsdk:"base_url"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &IsHostingProvider{
			version: version,
		}
	}
}

func (p *IsHostingProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "ishosting"
	resp.Version = p.version
}

func (p *IsHostingProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Interact with the ISHosting API to manage VPS instances and related resources.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				Description: "ISHosting API token. Can also be set via the ISHOSTING_API_TOKEN environment variable.",
				Optional:    true,
				Sensitive:   true,
			},
			"base_url": schema.StringAttribute{
				Description: "ISHosting API base URL. Defaults to https://api.ishosting.com.",
				Optional:    true,
			},
		},
	}
}

func (p *IsHostingProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config IsHostingProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Resolve API token
	apiToken := os.Getenv("ISHOSTING_API_TOKEN")
	if !config.APIToken.IsNull() {
		apiToken = config.APIToken.ValueString()
	}

	if apiToken == "" {
		resp.Diagnostics.AddAttributeError(
			path.Root("api_token"),
			"Missing ISHosting API Token",
			"The provider cannot create the ISHosting API client because the API token is missing. "+
				"Set the api_token value in the provider configuration or use the ISHOSTING_API_TOKEN environment variable.",
		)
		return
	}

	// Resolve base URL
	baseURL := os.Getenv("ISHOSTING_BASE_URL")
	if !config.BaseURL.IsNull() {
		baseURL = config.BaseURL.ValueString()
	}

	c := client.NewClient(apiToken, baseURL)

	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *IsHostingProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		resources.NewVPSResource,
		resources.NewSSHKeyResource,
		resources.NewVPSReinstallResource,
	}
}

func (p *IsHostingProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		datasources.NewVPSPlansDataSource,
		datasources.NewVPSConfigsDataSource,
		datasources.NewVPSIPsDataSource,
	}
}
