package resources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/filipegalo/terraform-provider-omada/internal/client"
)

var (
	_ resource.Resource                = &siteResource{}
	_ resource.ResourceWithConfigure   = &siteResource{}
	_ resource.ResourceWithImportState = &siteResource{}
)

// siteNameMaxLength is the longest site name the controller accepts.
const siteNameMaxLength = 64

// The provider is bound to a site that has to exist before it can be
// configured, so this resource never creates or deletes one. Create takes
// over the existing site; Delete deliberately only relinquishes Terraform
// management.
type siteResource struct {
	client        *client.Client
	defaultSiteID string
}

// NewSiteResource returns the omada_site resource implementation.
func NewSiteResource() resource.Resource { return &siteResource{} }

type siteModel struct {
	ID     types.String `tfsdk:"id"`
	SiteID types.String `tfsdk:"site_id"`
	Name   types.String `tfsdk:"name"`
}

func (r *siteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

func (r *siteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Settings of an existing Omada site, currently its name. The site must already exist: creating this resource takes over the site, and removing it only stops Terraform management; a site is never created or deleted. Every other site setting (region, time zone, NTP, DST, scenario, location) is left as it is.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, Description: "Site ID.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"site_id": schema.StringAttribute{Optional: true, Computed: true, Description: "Site ID. Defaults to the provider site.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}},
			"name": schema.StringAttribute{
				Required:    true,
				Description: fmt.Sprintf("Site name, 1 to %d characters. When the provider's own site is renamed, configure the provider's site by ID: a provider configured with the previous name can no longer resolve the site.", siteNameMaxLength),
				Validators:  []validator.String{stringvalidator.UTF8LengthBetween(1, siteNameMaxLength)},
			},
		},
	}
}

func (r *siteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*client.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.ProviderData, got: %T", req.ProviderData))
		return
	}
	r.client, r.defaultSiteID = data.Client, data.SiteID
}

func (r *siteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	siteID := resourceSiteID(plan.SiteID, r.defaultSiteID)
	if err := r.configure(ctx, &plan, siteID); err != nil {
		resp.Diagnostics.AddError("Unable to configure site", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.client.FindSite(ctx, state.SiteID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read site", err.Error())
		return
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID, state.Name = types.StringValue(found.ID), types.StringValue(found.Name)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *siteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.configure(ctx, &plan, plan.SiteID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to update site", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *siteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Import ID", "Expected the site ID or the site name as import ID, got an empty string.")
		return
	}
	siteID, err := r.client.ResolveSite(ctx, req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Unable to resolve site", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site_id"), siteID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), siteID)...)
}

// configure brings an existing site to the planned name. A site that already
// carries the name is taken over without any write; otherwise the client
// rewrites the site with everything but the name exactly as it was read.
func (r *siteResource) configure(ctx context.Context, plan *siteModel, siteID string) error {
	name := plan.Name.ValueString()
	found, err := r.client.FindSite(ctx, siteID)
	if err != nil {
		return err
	}
	if found == nil {
		return fmt.Errorf("no site with ID %q; this resource manages an existing site and does not create one", siteID)
	}
	if found.Name != name {
		if err := r.client.RenameSite(ctx, siteID, name); err != nil {
			return err
		}
		updated, err := r.client.FindSite(ctx, siteID)
		if err != nil {
			return fmt.Errorf("reading site after update: %w", err)
		}
		if updated == nil {
			return fmt.Errorf("site %s was not returned after update", siteID)
		}
		if updated.Name != name {
			return fmt.Errorf("the controller stored the site name %q instead of %q", updated.Name, name)
		}
	}
	plan.ID, plan.SiteID = types.StringValue(siteID), types.StringValue(siteID)
	return nil
}
