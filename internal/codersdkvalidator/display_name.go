package codersdkvalidator

import (
	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func DisplayName() validator.String {
	return validatorFromFunc(codersdk.DisplayNameValid, "value must be a valid display name")
}
