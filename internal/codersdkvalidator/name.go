package codersdkvalidator

import (
	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func Name() validator.String {
	return validatorFromFunc(codersdk.NameValid, "value must be a valid name")
}
