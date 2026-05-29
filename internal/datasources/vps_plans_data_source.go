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
	_ datasource.DataSource              = &VPSPlansDataSource{}
	_ datasource.DataSourceWithConfigure = &VPSPlansDataSource{}
)

type VPSPlansDataSource struct {
	client *client.Client
}

type VPSPlansDataSourceModel struct {
	Locations types.List  `tfsdk:"locations"`
	Platforms types.List  `tfsdk:"platforms"`
	Plans     []PlanModel `tfsdk:"plans"`
}

type PlanModel struct {
	Code         types.String `tfsdk:"code"`
	Name         types.String `tfsdk:"name"`
	Category     types.String `tfsdk:"category"`
	Price        types.String `tfsdk:"price"`
	Period       types.String `tfsdk:"period"`
	LocationName types.String `tfsdk:"location_name"`
	LocationCode types.String `tfsdk:"location_code"`
	PlatformName types.String `tfsdk:"platform_name"`
	PlatformCode types.String `tfsdk:"platform_code"`
	CPU          types.String `tfsdk:"cpu"`
	RAM          types.String `tfsdk:"ram"`
	Drive        types.String `tfsdk:"drive"`
	OS           types.String `tfsdk:"os"`
}

func NewVPSPlansDataSource() datasource.DataSource {
	return &VPSPlansDataSource{}
}

func (d *VPSPlansDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vps_plans"
}

func (d *VPSPlansDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists available ISHosting VPS plans.",
		Attributes: map[string]schema.Attribute{
			"locations": schema.ListAttribute{
				Description: "Filter by ISO country codes (e.g. [\"NL\", \"DE\"]). Each plan is tied to a single country.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"platforms": schema.ListAttribute{
				Description: "Filter by platform codes (e.g. [\"linux\"]).",
				Optional:    true,
				ElementType: types.StringType,
			},
			"plans": schema.ListNestedAttribute{
				Description: "Available VPS plans.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"code": schema.StringAttribute{
							Description: "Plan code (use this as the `plan` in the ishosting_vps resource). The code encodes the tier, country and billing period (e.g. \"29_1m\").",
							Computed:    true,
						},
						"name": schema.StringAttribute{
							Description: "Plan display name.",
							Computed:    true,
						},
						"category": schema.StringAttribute{
							Description: "Plan tier code (e.g. \"lite\", \"medium\").",
							Computed:    true,
						},
						"price": schema.StringAttribute{
							Description: "Plan price, e.g. \"6.99$\".",
							Computed:    true,
						},
						"period": schema.StringAttribute{
							Description: "Billing period code (e.g. \"1m\", \"1y\").",
							Computed:    true,
						},
						"location_name": schema.StringAttribute{
							Description: "Country name.",
							Computed:    true,
						},
						"location_code": schema.StringAttribute{
							Description: "ISO country code (e.g. \"NL\").",
							Computed:    true,
						},
						"platform_name": schema.StringAttribute{
							Description: "Platform name (e.g., Linux, Windows).",
							Computed:    true,
						},
						"platform_code": schema.StringAttribute{
							Description: "Platform code.",
							Computed:    true,
						},
						"cpu": schema.StringAttribute{
							Description: "CPU description (e.g. \"Xeon 2.90 GHz\").",
							Computed:    true,
						},
						"ram": schema.StringAttribute{
							Description: "RAM description (e.g. \"1 Gb\").",
							Computed:    true,
						},
						"drive": schema.StringAttribute{
							Description: "Drive description (e.g. \"20GB NVMe\").",
							Computed:    true,
						},
						"os": schema.StringAttribute{
							Description: "Default OS code (e.g. \"linux/ubuntu22#64\").",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *VPSPlansDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *VPSPlansDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state VPSPlansDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var locations, platforms []string
	if !state.Locations.IsNull() {
		resp.Diagnostics.Append(state.Locations.ElementsAs(ctx, &locations, false)...)
	}
	if !state.Platforms.IsNull() {
		resp.Diagnostics.Append(state.Platforms.ElementsAs(ctx, &platforms, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	plans, err := d.client.ListVPSPlans(ctx, locations, platforms)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading VPS Plans",
			"Could not read VPS plans: "+err.Error(),
		)
		return
	}

	state.Plans = make([]PlanModel, len(plans))
	for i, p := range plans {
		state.Plans[i] = PlanModel{
			Code:         types.StringValue(p.Plan.Code),
			Name:         types.StringValue(p.Plan.Name),
			Category:     types.StringValue(p.Plan.Category.Code),
			Price:        types.StringValue(p.Plan.Price),
			Period:       types.StringValue(p.Plan.Period.Code),
			LocationName: types.StringValue(p.Location.Name),
			LocationCode: types.StringValue(p.Location.Code),
			PlatformName: types.StringValue(p.Platform.Name),
			PlatformCode: types.StringValue(p.Platform.Code),
			CPU:          types.StringValue(p.Platform.Config.CPU.Name),
			RAM:          types.StringValue(p.Platform.Config.RAM.Name),
			Drive:        types.StringValue(p.Platform.Config.Drive.Name),
			OS:           types.StringValue(p.Platform.Config.OS.Name),
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
