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

func (r *AIModelPriceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ai_model_price"
}

func (r *AIModelPriceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	resp.Diagnostics.AddWarning(
		"Experimental Resource",
		"coderd_ai_model_price is experimental. Changes are expected, and it is not recommended for production use.",
	)
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
			"Sets a custom token price for a model, which AI Gateway uses to compute the cost of each request. " +
			"This resource requires a Premium license with the AI Gateway feature, and the Owner role.\n\n" +
			"Prices are integer micro-units per 1M tokens. For example, `3000000` is $3.00 per 1M tokens.\n\n" +
			"A price applies to every AI provider of `provider_type`. " +
			"`model` must match the model name that AI Gateway records for requests, as shown in AI Gateway usage and spend reports.\n\n" +
			"Coder ships default prices for many models. A custom price takes precedence over the default price for the same model. " +
			"Creating this resource overwrites any custom price that already exists for the same `provider_type` and `model`.\n\n" +
			"~> **Warning**\nCoder has no API to delete a custom price. Destroying this resource only removes it from the Terraform state. The custom price stays in Coder and still takes precedence over the default price.\n\n" +
			"Import IDs use `<provider_type>/<model>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Model price ID, in the form `<provider_type>/<model>`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"provider_type": schema.StringAttribute{
				MarkdownDescription: "AI provider type the price applies to. Valid values are `openai`, `anthropic`, `azure`, `bedrock`, `google`, `openrouter`, `vercel`, and `copilot`. Changing this forces a new resource.",
				Required:            true,
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
				MarkdownDescription: "Model name the price applies to, for example `claude-sonnet-4-5`. Changing this forces a new resource.",
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
	if err := r.upsert(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to set AI model price %s, got error: %s", id, err))
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
	prices, err := r.experimentalClient().ListAIModelPrices(ctx, codersdk.AIModelPricesFilter{
		Provider: providerType,
		Model:    model,
		Source:   codersdk.AIModelPriceSourceFilterCustom,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read AI model price %s, got error: %s", id, err))
		return
	}
	if len(prices) == 0 {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Custom AI model price %s not found. Marking as deleted.", id))
		resp.State.RemoveResource(ctx)
		return
	}

	price := prices[0]
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
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update AI model price %s, got error: %s", id, err))
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
