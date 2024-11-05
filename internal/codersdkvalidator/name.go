package codersdkvalidator

import (
	"context"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type nameValidator struct {
	err error
}

func Name() validator.String {
	return nameValidator{}
}

var _ validator.String = nameValidator{}

func (v nameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	if v.err = codersdk.NameValid(name); v.err != nil {
		resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
			req.Path,
			v.Description(ctx),
			name,
		))
	}
}

var _ validator.Describer = nameValidator{}

func (v nameValidator) Description(_ context.Context) string {
	if v.err != nil {
		return v.err.Error()
	}
	return "value must be a valid name"
}

func (v nameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
