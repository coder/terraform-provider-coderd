package codersdkvalidator

import (
	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func TemplateVersionName() validator.String {
	return validatorFromFunc(codersdk.TemplateVersionNameValid, "value must be a valid template version name")
}
