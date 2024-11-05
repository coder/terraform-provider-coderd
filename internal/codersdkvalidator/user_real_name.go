package codersdkvalidator

import (
	"context"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type userRealNameValidator struct {
	err error
}

func UserRealName() validator.String {
	return userRealNameValidator{}
}

var _ validator.String = userRealNameValidator{}

func (v userRealNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	if v.err = codersdk.UserRealNameValid(name); v.err != nil {
		resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
			req.Path,
			v.Description(ctx),
			name,
		))
	}
}

var _ validator.Describer = userRealNameValidator{}

func (v userRealNameValidator) Description(_ context.Context) string {
	if v.err != nil {
		return v.err.Error()
	}
	return "value must be a valid name for a user"
}

func (v userRealNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
