package codersdkvalidator

import (
	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func UserRealName() validator.String {
	return validatorFromFunc(codersdk.UserRealNameValid, "value must be a valid name for a user")
}
