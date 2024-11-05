package codersdkvalidator

import (
	"context"

	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework-validators/helpers/validatordiag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type templateVersionNameValidator struct {
	err error
}

func TemplateVersionName() validator.String {
	return templateVersionNameValidator{}
}

var _ validator.String = templateVersionNameValidator{}

func (v templateVersionNameValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	name := req.ConfigValue.ValueString()
	if v.err = codersdk.TemplateVersionNameValid(name); v.err != nil {
		resp.Diagnostics.Append(validatordiag.InvalidAttributeValueDiagnostic(
			req.Path,
			v.Description(ctx),
			name,
		))
	}
}

var _ validator.Describer = templateVersionNameValidator{}

func (v templateVersionNameValidator) Description(_ context.Context) string {
	if v.err != nil {
		return v.err.Error()
	}
	return "value must be a valid template version name"
}

func (v templateVersionNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
