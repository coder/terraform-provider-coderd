package internal

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

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

func DirectoryHash(directory string) (string, error) {
	hash := sha256.New()
	count := 0

	// filepath.Walk always proceeds in lexical order, so we don't need to worry
	// about order variance from call to call producing different hash results.
	err := filepath.Walk(directory, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		count++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hash.Write([]byte(path))
		hash.Write(data)

		return nil
	})

	if err != nil {
		return "", err
	}

	hash.Write([]byte(strconv.Itoa(count)))

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// MemberDiff returns the members to add and remove from the group, given the current members and the planned members.
// plannedMembers is deliberately our custom type, as Terraform cannot automatically produce `[]uuid.UUID` from a set.
func MemberDiff(curMembers []uuid.UUID, plannedMembers []UUID) (add, remove []string) {
	curSet := make(map[uuid.UUID]struct{}, len(curMembers))
	planSet := make(map[uuid.UUID]struct{}, len(plannedMembers))

	for _, userID := range curMembers {
		curSet[userID] = struct{}{}
	}
	for _, plannedUserID := range plannedMembers {
		planSet[plannedUserID.ValueUUID()] = struct{}{}
		if _, exists := curSet[plannedUserID.ValueUUID()]; !exists {
			add = append(add, plannedUserID.ValueString())
		}
	}
	for _, curUserID := range curMembers {
		if _, exists := planSet[curUserID]; !exists {
			remove = append(remove, curUserID.String())
		}
	}
	return add, remove
}

func IsNotFound(err error) bool {
	var sdkErr *codersdk.Error
	return errors.As(err, &sdkErr) && sdkErr.StatusCode() == http.StatusNotFound
}
