package provider

import (
	"fmt"
)

func PtrTo[T any](v T) *T {
	return &v
}

func PrintOrNull(v any) string {
	if v == nil {
		return "null"
	}
	switch value := v.(type) {
	case *int32:
		if value == nil {
			return "null"
		}
		return fmt.Sprintf("%d", *value)
	case *string:
		if value == nil {
			return "null"
		}
		out := fmt.Sprintf("%q", *value)
		return out
	case *bool:
		if value == nil {
			return "null"
		}
		return fmt.Sprintf(`%t`, *value)
	case *[]string:
		if value == nil {
			return "null"
		}
		var result string
		for i, role := range *value {
			if i > 0 {
				result += ", "
			}
			result += fmt.Sprintf("%q", role)
		}
		return fmt.Sprintf("[%s]", result)

	default:
		panic(fmt.Errorf("unknown type in template: %T", value))
	}
}
