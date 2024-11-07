package codersdkvalidator

import (
	"github.com/coder/coder/v2/codersdk"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func GroupName() validator.String {
	return validatorFromFunc(codersdk.GroupNameValid, "value must be a valid group name")
}
