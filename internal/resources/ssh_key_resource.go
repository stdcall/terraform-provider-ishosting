package resources

import (
	"context"
	"fmt"

	"terraform-provider-ishosting/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &SSHKeyResource{}
	_ resource.ResourceWithConfigure   = &SSHKeyResource{}
	_ resource.ResourceWithImportState = &SSHKeyResource{}
)

type SSHKeyResource struct {
	client *client.Client
}

type SSHKeyResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Title       types.String `tfsdk:"title"`
	PublicKey   types.String `tfsdk:"public_key"`
	Fingerprint types.String `tfsdk:"fingerprint"`
}

func NewSSHKeyResource() resource.Resource {
	return &SSHKeyResource{}
}

func (r *SSHKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *SSHKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an ISHosting SSH key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "SSH key ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"title": schema.StringAttribute{
				Description: "SSH key title/name.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"public_key": schema.StringAttribute{
				Description: "SSH public key content.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fingerprint": schema.StringAttribute{
				Description: "SSH key fingerprint.",
				Computed:    true,
			},
		},
	}
}

func (r *SSHKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ImportState lets `terraform import ishosting_ssh_key.<addr> <key-id>` adopt
// an existing account SSH key into state by its ID.
func (r *SSHKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *SSHKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SSHKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sshKey, err := r.client.CreateSSHKey(ctx, client.SSHKeyCreateRequest{
		Title:  plan.Title.ValueString(),
		Public: plan.PublicKey.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating SSH Key",
			"Could not create SSH key: "+err.Error(),
		)
		return
	}

	plan.ID = types.StringValue(sshKey.ID.String())
	plan.Fingerprint = types.StringValue(sshKey.Fingerprint)

	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *SSHKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SSHKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sshKey, err := r.client.GetSSHKey(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading SSH Key",
			"Could not read SSH key ID "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}

	state.ID = types.StringValue(sshKey.ID.String())
	state.Title = types.StringValue(sshKey.Title)
	state.Fingerprint = types.StringValue(sshKey.Fingerprint)
	if sshKey.Public != "" {
		state.PublicKey = types.StringValue(sshKey.Public)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SSHKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// SSH keys are immutable - title and public_key both RequiresReplace
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"SSH keys cannot be updated in place. Delete and recreate.",
	)
}

func (r *SSHKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SSHKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteSSHKey(ctx, state.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Deleting SSH Key",
			"Could not delete SSH key "+state.ID.ValueString()+": "+err.Error(),
		)
		return
	}
}
