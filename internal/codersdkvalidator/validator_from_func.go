package codersdkvalidator

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type functionValidator struct {
	check          func(string) error
	defaultMessage string
	err            error
}

func validatorFromFunc(check func(string) error, defaultMessage string) functionValidator {
	return functionValidator{
		check:          check,
		defaultMessage: defaultMessage,
	}
}

var _ validator.String = functionValidator{}

func (v functionValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	if v.err = v.check(name); v.err != nil {
		resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
			req.Path,
			v.Description(ctx),
			name,
		))
	}
}

var _ validator.Describer = functionValidator{}

func (v functionValidator) Description(_ context.Context) string {
	if v.err != nil {
		return v.err.Error()
	}
	return v.defaultMessage
}

func (v functionValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
