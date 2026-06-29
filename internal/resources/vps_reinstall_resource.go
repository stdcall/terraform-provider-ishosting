package resources

import (
	"context"
	"fmt"

	"terraform-provider-ishosting/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource              = &VPSReinstallResource{}
	_ resource.ResourceWithConfigure = &VPSReinstallResource{}
)

// VPSReinstallResource models a one-shot OS-reinstall action keyed by
// trigger string. Whenever `trigger` changes, the resource is replaced
// → Create runs again → API reinstall fires. The VPS itself
// (`ishosting_vps`) keeps its id + reserved IP across reinstalls; only
// disk + ssh access state are wiped.
//
// Use to bootstrap a new operator user (e.g. `nixos`) on an
// already-provisioned VPS, or to reset the OS without taint+recreate
// of the VPS resource.
type VPSReinstallResource struct {
	client *client.Client
}

// VPSReinstallSSHUserModel matches the API's user object.
type VPSReinstallSSHUserModel struct {
	Username  types.String `tfsdk:"username"`
	IsEnabled types.Bool   `tfsdk:"is_enabled"`
}

type VPSReinstallResourceModel struct {
	VPSID   types.String `tfsdk:"vps_id"`
	OSCode  types.String `tfsdk:"os_code"`
	Trigger types.String `tfsdk:"trigger"`

	SSHKeys  types.List                 `tfsdk:"ssh_keys"`
	SSHUsers []VPSReinstallSSHUserModel `tfsdk:"ssh_users"`
}

func NewVPSReinstallResource() resource.Resource {
	return &VPSReinstallResource{}
}

func (r *VPSReinstallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vps_reinstall"
}

func (r *VPSReinstallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	requiresReplace := []planmodifier.String{stringplanmodifier.RequiresReplace()}

	resp.Schema = schema.Schema{
		Description: "Triggers OS reinstall on an existing VPS via PATCH /vps/{id}/os. " +
			"Destructive: wipes disk + ssh users; the VPS id and reserved IP survive. " +
			"Replace this resource (e.g. by bumping `trigger`) to fire a new reinstall.",

		Attributes: map[string]schema.Attribute{
			"vps_id": schema.StringAttribute{
				Description:   "ID of the ishosting_vps to reinstall.",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"os_code": schema.StringAttribute{
				Description:   "OS code as exposed by the API (e.g. `linux/ubuntu22#64`). Read from `vps.platform.config.os.code`.",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"trigger": schema.StringAttribute{
				Description:   "Free-form value; bump to force a new reinstall (e.g. `\"add-nixos-user-v1\"`).",
				Required:      true,
				PlanModifiers: requiresReplace,
			},
			"ssh_keys": schema.ListAttribute{
				Description:   "SSH key IDs to seed into the freshly installed OS. Use `ishosting_ssh_key.<name>.id`.",
				Optional:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.List{},
			},
			"ssh_users": schema.ListNestedAttribute{
				Description: "User accounts to seed in the freshly installed OS.",
				Optional:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"username":   schema.StringAttribute{Required: true},
						"is_enabled": schema.BoolAttribute{Required: true},
					},
				},
			},
		},
	}
}

func (r *VPSReinstallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VPSReinstallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan VPSReinstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiReq := client.VPSReinstallRequest{}
	apiReq.OS.Code = plan.OSCode.ValueString()

	if !plan.SSHKeys.IsNull() || len(plan.SSHUsers) > 0 {
		ssh := &client.VPSReinstallSSH{}

		if !plan.SSHKeys.IsNull() {
			var keys []string
			resp.Diagnostics.Append(plan.SSHKeys.ElementsAs(ctx, &keys, false)...)
			if resp.Diagnostics.HasError() {
				return
			}
			ssh.Keys = keys
		}

		for _, u := range plan.SSHUsers {
			ssh.Users = append(ssh.Users, client.VPSReinstallSSHUser{
				Username:  u.Username.ValueString(),
				IsEnabled: u.IsEnabled.ValueBool(),
			})
		}

		apiReq.SSH = ssh
	}

	if _, err := r.client.ReinstallVPSOS(ctx, plan.VPSID.ValueString(), apiReq); err != nil {
		resp.Diagnostics.AddError(
			"Reinstall API Error",
			fmt.Sprintf("PATCH /vps/%s/os failed: %s", plan.VPSID.ValueString(), err),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

// Read is a no-op: the reinstall is a one-shot side-effect; nothing
// on the API side persists that we'd round-trip into state. State
// reflects the trigger value and is otherwise opaque.
func (r *VPSReinstallResource) Read(_ context.Context, _ resource.ReadRequest, _ *resource.ReadResponse) {
}

// Update is unreachable in practice: every input attribute has
// RequiresReplace, so changes destroy + recreate (= run Create =
// trigger reinstall). Defined for interface completeness.
func (r *VPSReinstallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"vps_reinstall is fire-and-forget; bump `trigger` to force a new reinstall (handled via destroy+create).",
	)
}

// Delete is a no-op: dropping the trigger record from state doesn't
// undo the reinstall (you can't un-wipe a disk).
func (r *VPSReinstallResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
