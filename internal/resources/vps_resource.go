package resources

import (
	"context"
	"fmt"
	"strings"

	"terraform-provider-ishosting/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ resource.Resource              = &VPSResource{}
	_ resource.ResourceWithConfigure = &VPSResource{}
)

type VPSResource struct {
	client *client.Client
}

type VPSResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	Tags      types.List   `tfsdk:"tags"`
	Plan      types.String `tfsdk:"plan"`
	Location  types.String `tfsdk:"location"`
	AutoRenew types.Bool   `tfsdk:"auto_renew"`

	// Order options
	OS         types.String `tfsdk:"os"`
	VNCEnabled types.Bool   `tfsdk:"vnc_enabled"`
	SSHEnabled types.Bool   `tfsdk:"ssh_enabled"`
	SSHKeys    types.List   `tfsdk:"ssh_keys"`
	Quantity   types.Int64  `tfsdk:"quantity"`
	Comment    types.String `tfsdk:"comment"`
	Promos     types.List   `tfsdk:"promos"`

	// Additions
	Additions types.List `tfsdk:"additions"`

	// Internal tracking
	InvoiceID types.String `tfsdk:"invoice_id"`

	// Computed
	PublicIP     types.String  `tfsdk:"public_ip"`
	Status       types.String  `tfsdk:"status"`
	State        types.String  `tfsdk:"state"`
	PlatformName types.String  `tfsdk:"platform_name"`
	CPUCores     types.Int64   `tfsdk:"cpu_cores"`
	RAMSize      types.Int64   `tfsdk:"ram_size"`
	RAMUnit      types.String  `tfsdk:"ram_unit"`
	DriveSize    types.Int64   `tfsdk:"drive_size"`
	DriveUnit    types.String  `tfsdk:"drive_unit"`
	DriveType    types.String  `tfsdk:"drive_type"`
	OSName       types.String  `tfsdk:"os_name"`
	OSVersion    types.String  `tfsdk:"os_version"`
	LocationName types.String  `tfsdk:"location_name"`
	PlanName     types.String  `tfsdk:"plan_name"`
	PlanPrice    types.Float64 `tfsdk:"plan_price"`
	CreatedAt    types.String  `tfsdk:"created_at"`
}

type AdditionModel struct {
	Code     types.String `tfsdk:"code"`
	Category types.String `tfsdk:"category"`
	Quantity types.Int64  `tfsdk:"quantity"`
}

func NewVPSResource() resource.Resource {
	return &VPSResource{}
}

func (r *VPSResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vps"
}

func (r *VPSResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an ISHosting VPS instance.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "VPS instance ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "VPS instance name.",
				Optional:    true,
				Computed:    true,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the VPS instance.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"plan": schema.StringAttribute{
				Description: "VPS plan code (e.g., '29_1m'). The code fully determines the country, billing period and base hardware. Use the ishosting_vps_plans data source (or ishosting_vps_configs.locations) to find available plan codes.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"location": schema.StringAttribute{
				Description: "ISO country code of the VPS (e.g., 'NL'). Derived from the plan code; this is a read-only computed value.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"auto_renew": schema.BoolAttribute{
				Description: "Whether to auto-renew the VPS.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},

			// Order options
			"os": schema.StringAttribute{
				Description: "OS image code from the plan configs (e.g., 'linux/ubuntu24#64', 'linux/debian12#64'). If omitted, the plan's default OS is used. Find available codes via the ishosting_vps_configs data source (platforms.additions.fixed.os).",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vnc_enabled": schema.BoolAttribute{
				Description: "Enable VNC access.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"ssh_enabled": schema.BoolAttribute{
				Description: "Enable SSH access.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"ssh_keys": schema.ListAttribute{
				Description: "List of SSH key IDs to attach.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"quantity": schema.Int64Attribute{
				Description: "Number of VPS instances to order.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(1),
			},
			"comment": schema.StringAttribute{
				Description: "Comment for the order.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(""),
			},
			"promos": schema.ListAttribute{
				Description: "Promo codes to apply.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"additions": schema.ListNestedAttribute{
				Description: "Additional configuration options such as extra RAM, larger drive, control panel, or additional IPs. Codes and categories come from the ishosting_vps_configs data source. Changing additions forces replacement.",
				Optional:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"category": schema.StringAttribute{
							Description: "Addition category code (e.g., 'ram', 'disk', 'panel', 'ip').",
							Required:    true,
						},
						"code": schema.StringAttribute{
							Description: "Addition option code (e.g., '2g' for RAM). Use either code or quantity depending on the category.",
							Optional:    true,
						},
						"quantity": schema.Int64Attribute{
							Description: "Quantity for quantity-based additions such as extra IPs (category 'ip').",
							Optional:    true,
						},
					},
				},
			},

			// Internal tracking
			"invoice_id": schema.StringAttribute{
				Description: "Invoice ID from the order. Used to cancel unpaid orders on destroy.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},

			// Computed attributes
			"public_ip": schema.StringAttribute{
				Description: "Primary public IP address.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "Current VPS status code.",
				Computed:    true,
			},
			"state": schema.StringAttribute{
				Description: "Current VPS state code.",
				Computed:    true,
			},
			"platform_name": schema.StringAttribute{
				Description: "Platform name (e.g., Linux, Windows).",
				Computed:    true,
			},
			"cpu_cores": schema.Int64Attribute{
				Description: "Number of CPU cores.",
				Computed:    true,
			},
			"ram_size": schema.Int64Attribute{
				Description: "RAM size.",
				Computed:    true,
			},
			"ram_unit": schema.StringAttribute{
				Description: "RAM unit (e.g., GB).",
				Computed:    true,
			},
			"drive_size": schema.Int64Attribute{
				Description: "Drive size.",
				Computed:    true,
			},
			"drive_unit": schema.StringAttribute{
				Description: "Drive unit (e.g., GB).",
				Computed:    true,
			},
			"drive_type": schema.StringAttribute{
				Description: "Drive type (e.g., SSD, NVMe).",
				Computed:    true,
			},
			"os_name": schema.StringAttribute{
				Description: "Operating system name.",
				Computed:    true,
			},
			"os_version": schema.StringAttribute{
				Description: "Operating system version.",
				Computed:    true,
			},
			"location_name": schema.StringAttribute{
				Description: "Location name.",
				Computed:    true,
			},
			"plan_name": schema.StringAttribute{
				Description: "Plan display name.",
				Computed:    true,
			},
			"plan_price": schema.Float64Attribute{
				Description: "Plan price.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "VPS creation timestamp.",
				Computed:    true,
			},
		},
	}
}

func (r *VPSResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = c
}

func (r *VPSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VPSResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The VPS provisions asynchronously after payment, so computed attributes are
	// not known at create time. Initialize them to null (a known value) so that no
	// unknown values remain in state after apply; a later refresh/read populates
	// them with the real values once the VPS is provisioned.
	plan.PublicIP = types.StringNull()
	plan.Status = types.StringNull()
	plan.State = types.StringNull()
	plan.Location = types.StringNull()
	plan.PlatformName = types.StringNull()
	plan.CPUCores = types.Int64Null()
	plan.RAMSize = types.Int64Null()
	plan.RAMUnit = types.StringNull()
	plan.DriveSize = types.Int64Null()
	plan.DriveUnit = types.StringNull()
	plan.DriveType = types.StringNull()
	plan.OSName = types.StringNull()
	plan.OSVersion = types.StringNull()
	plan.LocationName = types.StringNull()
	plan.PlanName = types.StringNull()
	plan.PlanPrice = types.Float64Null()
	plan.CreatedAt = types.StringNull()
	if plan.Name.IsUnknown() {
		plan.Name = types.StringNull()
	}

	// Build order item. The plan code already determines the location, so no
	// separate location field is sent. Identity correlates the item within the
	// order, matching the official API client.
	orderItem := client.OrderItem{
		Action:   "new",
		Identity: client.NewOrderIdentity(),
		Type:     "vps",
		Plan:     plan.Plan.ValueString(),
		Quantity: int(plan.Quantity.ValueInt64()),
		Comment:  plan.Comment.ValueString(),
	}

	// Options
	options := &client.OrderOptions{}
	options.VNC = &client.OrderVNC{
		IsEnabled: plan.VNCEnabled.ValueBool(),
	}
	options.SSH = &client.OrderSSH{
		IsEnabled: plan.SSHEnabled.ValueBool(),
	}

	// SSH Keys
	if !plan.SSHKeys.IsNull() {
		var sshKeys []string
		resp.Diagnostics.Append(plan.SSHKeys.ElementsAs(ctx, &sshKeys, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		options.SSH.Keys = sshKeys
	}
	orderItem.Options = options

	// Additions
	var additions []client.OrderAddition
	if !plan.Additions.IsNull() {
		var additionModels []AdditionModel
		resp.Diagnostics.Append(plan.Additions.ElementsAs(ctx, &additionModels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, a := range additionModels {
			add := client.OrderAddition{
				Code:     a.Code.ValueString(),
				Category: a.Category.ValueString(),
			}
			if !a.Quantity.IsNull() {
				q := int(a.Quantity.ValueInt64())
				add.Quantity = &q
			}
			additions = append(additions, add)
		}
	}

	// Add OS override if specified. The OS category is always "os"; the code is
	// the full image code (e.g. "linux/ubuntu24#64").
	if !plan.OS.IsNull() && plan.OS.ValueString() != "" {
		additions = append(additions, client.OrderAddition{
			Code:     plan.OS.ValueString(),
			Category: "os",
		})
	}

	orderItem.Additions = additions

	// Build order request
	orderReq := client.OrderRequest{
		Items: []client.OrderItem{orderItem},
	}

	// Promos
	if !plan.Promos.IsNull() {
		var promos []string
		resp.Diagnostics.Append(plan.Promos.ElementsAs(ctx, &promos, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		orderReq.Promos = promos
	}

	tflog.Debug(ctx, "Creating VPS order")

	// Lock the order mutex to ensure only one order (cart) is processed at a time.
	// Hold the lock until the VPS is active so a concurrent order can't interfere.
	r.client.LockOrder()
	defer r.client.UnlockOrder()

	invoiceResp, err := r.client.CreateOrder(ctx, orderReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating VPS",
			"Could not create VPS order: "+err.Error(),
		)
		return
	}

	// Save invoice ID to state immediately so destroy can cancel it if payment fails
	plan.InvoiceID = types.StringValue(invoiceResp.ID.String())

	// Extract VPS ID from the invoice services
	var vpsID string
	for _, svc := range invoiceResp.Services {
		if svc.Type == "vps" {
			vpsID = svc.Service.ID.String()
			break
		}
	}

	if vpsID == "" {
		// Save state with invoice ID so destroy can cancel the invoice
		resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
		resp.Diagnostics.AddError(
			"Error Creating VPS",
			"No VPS service ID returned from order response.",
		)
		return
	}

	// Save state with VPS ID + invoice ID before payment, so destroy can clean up
	plan.ID = types.StringValue(vpsID)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)

	// Pay the invoice
	tflog.Debug(ctx, fmt.Sprintf("Paying invoice %s", invoiceResp.ID.String()))

	// Auto-renew for new orders is controlled at payment time via the renew flag,
	// not via a service edit (which can fail on a pending/unpaid service).
	payResp, err := r.client.PayInvoice(ctx, invoiceResp.ID.String(), client.PayInvoiceRequest{
		Balance: true,
		Renew:   plan.AutoRenew.ValueBool(),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Paying Invoice",
			fmt.Sprintf("Could not pay invoice %s: %s. Run 'terraform destroy' to cancel the unpaid order.", invoiceResp.ID.String(), err.Error()),
		)
		return
	}

	tflog.Debug(ctx, fmt.Sprintf("Invoice payment response: %s", string(payResp)))
	tflog.Debug(ctx, fmt.Sprintf("VPS ordered and paid, ID: %s. VPS will be provisioned shortly.", vpsID))

	// State is already saved with VPS ID + invoice ID from above.
	// The VPS will be provisioned asynchronously — run 'terraform refresh' to update computed fields.
}

func (r *VPSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state VPSResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If the VPS ID is empty, the order was created but never paid/provisioned.
	// Keep the state as-is so destroy can cancel the invoice.
	if state.ID.IsNull() || state.ID.ValueString() == "" {
		return
	}

	vps, err := r.client.GetVPS(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading VPS",
			"Could not read VPS ID "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	r.mapVPSToModel(vps, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VPSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan VPSResourceModel
	var state VPSResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patchReq := client.VPSPatchRequest{}

	// The API requires `name` on every PATCH, even when it is unchanged
	// (omitting it returns 400 "should have required property 'name'").
	name := plan.Name.ValueString()
	if plan.Name.IsNull() || plan.Name.IsUnknown() {
		name = state.Name.ValueString()
	}
	patchReq.Name = &name

	// Tags are always required by the API in PATCH requests
	var tags []string
	if !plan.Tags.IsNull() {
		resp.Diagnostics.Append(plan.Tags.ElementsAs(ctx, &tags, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if tags == nil {
		tags = []string{}
	}
	patchReq.Tags = tags

	// Update auto_renew if changed
	if !plan.AutoRenew.Equal(state.AutoRenew) {
		autoRenew := plan.AutoRenew.ValueBool()
		patchReq.Plan = &struct {
			AutoRenew *bool `json:"auto_renew,omitempty"`
		}{
			AutoRenew: &autoRenew,
		}
	}

	_, err := r.client.UpdateVPS(ctx, state.ID.ValueString(), patchReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Updating VPS",
			"Could not update VPS "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	// Read back the full state
	vps, err := r.client.GetVPS(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading VPS After Update",
			"Could not read VPS "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	r.mapVPSToModel(vps, &plan)
	// invoice_id is only known at create time and is not part of the VPS read
	// response, so carry it over from prior state to keep it a known value
	// (otherwise it stays unknown after apply, which Terraform rejects).
	plan.InvoiceID = state.InvoiceID
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *VPSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state VPSResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If the VPS was never paid for (no state/status), cancel the invoice instead
	if state.State.IsNull() || state.State.ValueString() == "" {
		if !state.InvoiceID.IsNull() && state.InvoiceID.ValueString() != "" {
			tflog.Debug(ctx, fmt.Sprintf("VPS not provisioned, cancelling invoice %s", state.InvoiceID.ValueString()))
			err := r.client.CancelInvoice(ctx, state.InvoiceID.ValueString())
			if err != nil {
				resp.Diagnostics.AddError(
					"Error Cancelling Invoice",
					fmt.Sprintf("Could not cancel invoice %s: %s", state.InvoiceID.ValueString(), err.Error()),
				)
				return
			}
			tflog.Debug(ctx, fmt.Sprintf("Invoice %s cancelled", state.InvoiceID.ValueString()))
			return
		}
		// No invoice ID and no VPS state — nothing to clean up
		return
	}

	// ISHosting does not support deleting VPS instances via the API.
	// Instead, disable auto-renew so the instance expires at the end of the billing period.
	autoRenew := false
	patchReq := client.VPSPatchRequest{
		Plan: &struct {
			AutoRenew *bool `json:"auto_renew,omitempty"`
		}{
			AutoRenew: &autoRenew,
		},
	}

	// The API requires `name` on every PATCH.
	name := state.Name.ValueString()
	patchReq.Name = &name

	// Tags are required in PATCH requests
	var tags []string
	if !state.Tags.IsNull() {
		state.Tags.ElementsAs(ctx, &tags, false)
	}
	if tags == nil {
		tags = []string{}
	}
	patchReq.Tags = tags

	_, err := r.client.UpdateVPS(ctx, state.ID.ValueString(), patchReq)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Disabling Auto-Renew",
			"Could not disable auto-renew for VPS "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	resp.Diagnostics.AddWarning(
		"VPS Not Deleted",
		fmt.Sprintf("ISHosting does not support deleting VPS instances. Auto-renew has been disabled for VPS %s. "+
			"The instance will be decommissioned at the end of the current billing period.", state.ID.ValueString()),
	)

	tflog.Warn(ctx, fmt.Sprintf("VPS %s: auto-renew disabled, instance will expire at end of billing period", state.ID.ValueString()))
}

func (r *VPSResource) mapVPSToModel(vps *client.VPS, model *VPSResourceModel) {
	model.ID = types.StringValue(vps.ID.String())
	model.Name = types.StringValue(vps.Name)
	model.PublicIP = types.StringValue(vps.Network.PublicIP)
	model.Status = types.StringValue(vps.Status.Code)
	model.State = types.StringValue(vps.Status.State.Code)
	model.PlatformName = types.StringValue(vps.Platform.Name)
	model.OSName = types.StringValue(vps.Platform.Config.OS.Name)
	model.LocationName = types.StringValue(vps.Location.Name)
	model.Location = types.StringValue(strings.ToLower(vps.Location.Code))
	model.PlanName = types.StringValue(vps.Plan.Name)
	model.Plan = types.StringValue(vps.Plan.Code)
	model.AutoRenew = types.BoolValue(vps.Plan.AutoRenew)
	model.CreatedAt = types.StringValue(vps.CreatedAt.String())

	// Config fields use value/name/code strings in the API, map the names
	model.CPUCores = types.Int64Value(0)
	model.RAMSize = types.Int64Value(0)
	model.RAMUnit = types.StringValue(vps.Platform.Config.RAM.Name)
	model.DriveSize = types.Int64Value(0)
	model.DriveUnit = types.StringValue(vps.Platform.Config.Drive.Name)
	model.DriveType = types.StringValue(vps.Platform.Config.Drive.Code)
	model.OSVersion = types.StringValue(vps.Platform.Config.OS.Code)
	model.PlanPrice = types.Float64Value(0)

	// Map tags
	if len(vps.Tags) > 0 {
		tags, _ := types.ListValueFrom(context.Background(), types.StringType, vps.Tags)
		model.Tags = tags
	}
}
