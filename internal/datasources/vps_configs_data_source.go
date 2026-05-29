package datasources

import (
	"context"
	"fmt"

	"terraform-provider-ishosting/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &VPSConfigsDataSource{}
	_ datasource.DataSourceWithConfigure = &VPSConfigsDataSource{}
)

type VPSConfigsDataSource struct {
	client *client.Client
}

type VPSConfigsDataSourceModel struct {
	PlanCode   types.String          `tfsdk:"plan_code"`
	ConfigJSON types.String          `tfsdk:"config_json"`
	Locations  []ConfigLocationModel `tfsdk:"locations"`
}

type ConfigLocationModel struct {
	Name  types.String `tfsdk:"name"`
	Code  types.String `tfsdk:"code"`
	Plan  types.String `tfsdk:"plan"`
	Price types.String `tfsdk:"price"`
}

func NewVPSConfigsDataSource() datasource.DataSource {
	return &VPSConfigsDataSource{}
}

func (d *VPSConfigsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vps_configs"
}

func (d *VPSConfigsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves available configuration options for a specific VPS plan. Returns a JSON string containing all available periods, locations, platforms, OS templates, network options, security options, and tools, plus the per-country plan codes as a structured list.",
		Attributes: map[string]schema.Attribute{
			"plan_code": schema.StringAttribute{
				Description: "The VPS plan code to get configuration options for.",
				Required:    true,
			},
			"config_json": schema.StringAttribute{
				Description: "JSON string containing the full configuration options for the plan. Parse this with jsondecode() to access nested values (e.g. platforms.additions.fixed.os for available OS codes).",
				Computed:    true,
			},
			"locations": schema.ListNestedAttribute{
				Description: "Per-country plan codes for this plan. Each country maps to a distinct plan code to use as the `plan` in the ishosting_vps resource.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "Country name.",
							Computed:    true,
						},
						"code": schema.StringAttribute{
							Description: "ISO country code (e.g. \"NL\").",
							Computed:    true,
						},
						"plan": schema.StringAttribute{
							Description: "Plan code for this country (use as the `plan` in ishosting_vps).",
							Computed:    true,
						},
						"price": schema.StringAttribute{
							Description: "Price for this country, e.g. \"6.99$\".",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *VPSConfigsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T.", req.ProviderData),
		)
		return
	}

	d.client = c
}

func (d *VPSConfigsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state VPSConfigsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	planCode := state.PlanCode.ValueString()
	configData, err := d.client.GetVPSConfigs(ctx, planCode)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading VPS Configs",
			"Could not read VPS configs for plan "+planCode+": "+err.Error(),
		)
		return
	}

	state.ConfigJSON = types.StringValue(string(configData))

	locations, err := client.ParseConfigLocations(configData)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Parsing VPS Configs",
			"Could not parse locations from VPS configs for plan "+planCode+": "+err.Error(),
		)
		return
	}
	state.Locations = make([]ConfigLocationModel, len(locations))
	for i, l := range locations {
		state.Locations[i] = ConfigLocationModel{
			Name:  types.StringValue(l.Name),
			Code:  types.StringValue(l.Code),
			Plan:  types.StringValue(l.Plan),
			Price: types.StringValue(l.Price),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
