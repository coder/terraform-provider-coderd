package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// aiModelPriceMinVersion is the first Coder release that includes the AI model
// prices API.
const aiModelPriceMinVersion = "2.37.0"

var (
	_ resource.Resource                     = &AIModelPriceResource{}
	_ resource.ResourceWithConfigure        = &AIModelPriceResource{}
	_ resource.ResourceWithImportState      = &AIModelPriceResource{}
	_ resource.ResourceWithModifyPlan       = &AIModelPriceResource{}
	_ resource.ResourceWithConfigValidators = &AIModelPriceResource{}
)

func NewAIModelPriceResource() resource.Resource {
	return &AIModelPriceResource{}
}

type AIModelPriceResource struct {
	data *CoderdProviderData
}

type AIModelPriceResourceModel struct {
	ID              types.String `tfsdk:"id"`
	ProviderType    types.String `tfsdk:"provider_type"`
	Model           types.String `tfsdk:"model"`
	InputPrice      types.Int64  `tfsdk:"input_price"`
	OutputPrice     types.Int64  `tfsdk:"output_price"`
	CacheReadPrice  types.Int64  `tfsdk:"cache_read_price"`
	CacheWritePrice types.Int64  `tfsdk:"cache_write_price"`
}

func CheckAIBridgeEntitlements(ctx context.Context, features map[codersdk.FeatureName]codersdk.Feature) (diags diag.Diagnostics) {
	if !features[codersdk.FeatureAIBridge].Enabled {
		diags.AddError("Feature not enabled", "Your license is not entitled to use AI Gateway.")
		return
	}
	return nil
}

func aiModelPriceID(providerType, model string) string {
	return providerType + "/" + model
}

func (r *AIModelPriceResource) experimentalClient() *codersdk.ExperimentalClient {
	return codersdk.NewExperimentalClient(r.data.Client)
}

// aiModelPriceDiag converts an error from the model prices API into a
// diagnostic. The list call returns an empty list and the upsert call never
// returns 404, so a 404 means that the deployment has no model prices API.
func aiModelPriceDiag(action, id string, err error) diag.Diagnostics {
	var diags diag.Diagnostics
	if isHTTPNotFound(err) {
		diags.AddError("Unsupported Coder Version", fmt.Sprintf(
			"Unable to %s AI model price %s: the deployment returned 404 for /api/experimental/ai/model-prices. "+
				"This resource requires Coder version %s or later; upgrade the deployment, or remove "+
				"`coderd_ai_model_price` from your configuration. Original error: %s",
			action, id, aiModelPriceMinVersion, err))
		return diags
	}
	diags.AddError("Client Error", fmt.Sprintf("Unable to %s AI model price %s, got error: %s", action, id, err))
	return diags
}

func aiModelPriceConflictDiag(id string) diag.Diagnostics {
	var diags diag.Diagnostics
	diags.AddError("AI Model Price Already Exists", fmt.Sprintf(
		"Coder already has a custom price for %s with different values. Terraform does not overwrite a price that it does not manage. "+
			"To manage the existing price, import it with the ID `%s`, then set the values that you want.",
		id, id))
	return diags
}

// customPrice returns nil, nil when Coder has no custom price for the key.
func (r *AIModelPriceResource) customPrice(ctx context.Context, providerType, model string) (*codersdk.AIModelPrice, error) {
	prices, err := r.experimentalClient().ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
		Provider: providerType,
		Model:    model,
		Source:   codersdk.AIModelPriceSourceFilterCustom,
	})
	if err != nil {
		return nil, err
	}
	if len(prices) == 0 {
		return nil, nil
	}
	return &prices[0], nil
}

// sameAIModelPrice reports whether the plan matches an existing custom price.
// A create adopts an equal price, because destroy leaves the price in Coder,
// and a strict check would then fail every destroy and re-apply.
func sameAIModelPrice(plan AIModelPriceResourceModel, p codersdk.AIModelPrice) bool {
	return plan.InputPrice.Equal(types.Int64PointerValue(p.InputPrice)) &&
		plan.OutputPrice.Equal(types.Int64PointerValue(p.OutputPrice)) &&
		plan.CacheReadPrice.Equal(types.Int64PointerValue(p.CacheReadPrice)) &&
		plan.CacheWritePrice.Equal(types.Int64PointerValue(p.CacheWritePrice))
}

func (r *AIModelPriceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ai_model_price"
}

func (r *AIModelPriceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_ai_model_price is experimental. Changes are expected, and it is not recommended for production use.",
	)

	if req.Plan.Raw.IsNull() || r.data == nil {
		return
	}
	var plan AIModelPriceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.ProviderType.IsUnknown() || plan.Model.IsUnknown() {
		return
	}
	// Only a create or a replace writes a key that Terraform does not manage yet.
	if !req.State.Raw.IsNull() {
		var state AIModelPriceResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if state.ProviderType.Equal(plan.ProviderType) && state.Model.Equal(plan.Model) {
			return
		}
	}
	id := aiModelPriceID(plan.ProviderType.ValueString(), plan.Model.ValueString())
	existing, err := r.customPrice(ctx, plan.ProviderType.ValueString(), plan.Model.ValueString())
	if isHTTPNotFound(err) {
		resp.Diagnostics.Append(aiModelPriceDiag("read", id, err)...)
		return
	}
	if err != nil {
		// Best-effort. Create() repeats the check and reports the error.
		tflog.Debug(ctx, "skipping AI model price plan-time conflict check", map[string]any{
			"error": err.Error(),
		})
		return
	}
	if existing == nil || plan.InputPrice.IsUnknown() || plan.OutputPrice.IsUnknown() ||
		plan.CacheReadPrice.IsUnknown() || plan.CacheWritePrice.IsUnknown() {
		return
	}
	if !sameAIModelPrice(plan, *existing) {
		resp.Diagnostics.Append(aiModelPriceConflictDiag(id)...)
	}
}

func (r *AIModelPriceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	priceDescription := func(tokens string) string {
		return "Price per 1M " + tokens + ", in integer micro-units: `1000000` is $1.00 per 1M tokens. " +
			"Omit it if the price is not known, which AI Gateway counts as zero cost. Set `0` to declare the tokens free of charge."
	}
	priceValidators := []validator.Int64{int64validator.AtLeast(0)}

	resp.Schema = schema.Schema{
		MarkdownDescription: "~> This resource is experimental. Changes are expected, and it is not recommended for production use.\n\n" +
			"~> **Warning**\nThis resource is only compatible with Coder version [" + aiModelPriceMinVersion + "](https://github.com/coder/coder/releases/tag/v" + aiModelPriceMinVersion + ") and later.\n\n" +
			"Manages the token price that AI Gateway uses to compute the cost of each request to a model. " +
			"Requires a Premium license with the AI Gateway feature, and the Owner role.\n\n" +
			"Coder ships [default prices](https://coder.com/docs/ai-coder/ai-gateway/cost-controls#configure-model-prices) for many models. " +
			"Use this resource to override a default price, or to price a model that has no default. " +
			"Coder lists the price as a `custom` price, which takes precedence over the default and stays in effect across Coder upgrades.\n\n" +
			"-> If Coder already has a different custom price for the same model, the plan fails. Import that price to manage it with Terraform.\n\n" +
			"~> **Warning**\nDestroying this resource only removes it from the Terraform state. The price stays in effect, because Coder has no API to delete a custom price.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Model price ID, in the form `<provider_type>/<model>`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"provider_type": schema.StringAttribute{
				MarkdownDescription: "AI provider type the price applies to. Valid values are `openai`, `anthropic`, `azure`, `bedrock`, `google`, `openrouter`, `vercel`, and `copilot`. " +
					"`openai-compat` is not supported, because these providers pass through to any upstream vendor. " +
					"Changing this forces a new resource.",
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						string(codersdk.AIProviderTypeOpenAI),
						string(codersdk.AIProviderTypeAnthropic),
						string(codersdk.AIProviderTypeAzure),
						string(codersdk.AIProviderTypeBedrock),
						string(codersdk.AIProviderTypeGoogle),
						string(codersdk.AIProviderTypeOpenrouter),
						string(codersdk.AIProviderTypeVercel),
						string(codersdk.AIProviderTypeCopilot),
					),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model": schema.StringAttribute{
				MarkdownDescription: "Model name the price applies to, as shown in AI Gateway usage and spend reports, for example `claude-sonnet-4-5`. Changing this forces a new resource.",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"input_price": schema.Int64Attribute{
				MarkdownDescription: priceDescription("input tokens"),
				Optional:            true,
				Validators:          priceValidators,
			},
			"output_price": schema.Int64Attribute{
				MarkdownDescription: priceDescription("output tokens"),
				Optional:            true,
				Validators:          priceValidators,
			},
			"cache_read_price": schema.Int64Attribute{
				MarkdownDescription: priceDescription("tokens read from the prompt cache"),
				Optional:            true,
				Validators:          priceValidators,
			},
			"cache_write_price": schema.Int64Attribute{
				MarkdownDescription: priceDescription("tokens written to the prompt cache"),
				Optional:            true,
				Validators:          priceValidators,
			},
		},
	}
}

func (r *AIModelPriceResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.AtLeastOneOf(
			path.MatchRoot("input_price"),
			path.MatchRoot("output_price"),
			path.MatchRoot("cache_read_price"),
			path.MatchRoot("cache_write_price"),
		),
	}
}

func (r *AIModelPriceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	data, ok := req.ProviderData.(*CoderdProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *CoderdProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}
	r.data = data
}

func (r *AIModelPriceResource) upsert(ctx context.Context, plan AIModelPriceResourceModel) error {
	return r.experimentalClient().UpsertAIModelPrices(ctx, codersdk.UpsertAIModelPricesRequest{
		Prices: []codersdk.AIModelPriceUpsert{{
			Provider:        plan.ProviderType.ValueString(),
			Model:           plan.Model.ValueString(),
			InputPrice:      plan.InputPrice.ValueInt64Pointer(),
			OutputPrice:     plan.OutputPrice.ValueInt64Pointer(),
			CacheReadPrice:  plan.CacheReadPrice.ValueInt64Pointer(),
			CacheWritePrice: plan.CacheWritePrice.ValueInt64Pointer(),
		}},
	})
}

func (r *AIModelPriceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AIModelPriceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := aiModelPriceID(plan.ProviderType.ValueString(), plan.Model.ValueString())
	existing, err := r.customPrice(ctx, plan.ProviderType.ValueString(), plan.Model.ValueString())
	if err != nil {
		resp.Diagnostics.Append(aiModelPriceDiag("read", id, err)...)
		return
	}
	if existing != nil && !sameAIModelPrice(plan, *existing) {
		resp.Diagnostics.Append(aiModelPriceConflictDiag(id)...)
		return
	}
	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiModelPriceDiag("set", id, err)...)
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIModelPriceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AIModelPriceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	providerType := state.ProviderType.ValueString()
	model := state.Model.ValueString()
	id := aiModelPriceID(providerType, model)
	price, err := r.customPrice(ctx, providerType, model)
	if err != nil {
		resp.Diagnostics.Append(aiModelPriceDiag("read", id, err)...)
		return
	}
	if price == nil {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Custom AI model price %s not found. Marking as deleted.", id))
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(id)
	state.InputPrice = types.Int64PointerValue(price.InputPrice)
	state.OutputPrice = types.Int64PointerValue(price.OutputPrice)
	state.CacheReadPrice = types.Int64PointerValue(price.CacheReadPrice)
	state.CacheWritePrice = types.Int64PointerValue(price.CacheWritePrice)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AIModelPriceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AIModelPriceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(CheckAIBridgeEntitlements(ctx, r.data.Features())...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := aiModelPriceID(plan.ProviderType.ValueString(), plan.Model.ValueString())
	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.Append(aiModelPriceDiag("update", id, err)...)
		return
	}

	plan.ID = types.StringValue(id)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AIModelPriceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state AIModelPriceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.AddWarning(
		"Custom AI Model Price Not Deleted",
		fmt.Sprintf("Coder has no API to delete a custom AI model price. The resource is removed from the Terraform state, but the custom price %s stays in Coder.", aiModelPriceID(state.ProviderType.ValueString(), state.Model.ValueString())),
	)
}

func (r *AIModelPriceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The model can carry a "/" (for example an OpenRouter model), so split
	// only on the first one.
	providerType, model, ok := strings.Cut(req.ID, "/")
	if !ok || providerType == "" || model == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected an import ID of the form <provider_type>/<model>, got %q.", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("provider_type"), providerType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model"), model)...)
}
