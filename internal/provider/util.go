package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/coder/coder/v2/codersdk"
	"github.com/google/uuid"
)

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
	case *int64:
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

func computeDirectoryHash(directory string) (string, error) {
	var files []string
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		hash.Write(data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// memberDiff returns the members to add and remove from the group, given the current members and the planned members.
// plannedMembers is deliberately our custom type, as Terraform cannot automatically produce `[]uuid.UUID` from a set.
func memberDiff(currentMembers []uuid.UUID, plannedMembers []UUID) (add, remove []string) {
	curSet := make(map[uuid.UUID]struct{}, len(currentMembers))
	planSet := make(map[uuid.UUID]struct{}, len(plannedMembers))

	for _, userID := range currentMembers {
		curSet[userID] = struct{}{}
	}
	for _, plannedUserID := range plannedMembers {
		planSet[plannedUserID.ValueUUID()] = struct{}{}
		if _, exists := curSet[plannedUserID.ValueUUID()]; !exists {
			add = append(add, plannedUserID.ValueString())
		}
	}
	for _, curUserID := range currentMembers {
		if _, exists := planSet[curUserID]; !exists {
			remove = append(remove, curUserID.String())
		}
	}
	return add, remove
}

func isNotFound(err error) bool {
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}
