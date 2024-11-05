package codersdkvalidator

import (
	"context"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type displayNameValidator struct {
	err error
}

func DisplayName() validator.String {
	return displayNameValidator{}
}

var _ validator.String = displayNameValidator{}

func (v displayNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	if v.err = codersdk.DisplayNameValid(name); v.err != nil {
		resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
			req.Path,
			v.Description(ctx),
			name,
		))
	}
}

var _ validator.Describer = displayNameValidator{}

func (v displayNameValidator) Description(_ context.Context) string {
	if v.err != nil {
		return v.err.Error()
	}
	return "value must be a valid display name"
}

func (v displayNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
