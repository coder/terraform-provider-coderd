package codersdkvalidator

import (
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

func checkRegexp(it string) error {
	_, err := regexp.Compile("")
	return err
}

func Regexp() validator.String {
	return validatorFromFunc(checkRegexp, "value must be a valid regexp")
}
