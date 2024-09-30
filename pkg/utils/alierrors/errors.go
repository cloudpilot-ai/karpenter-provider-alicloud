package alierrors

import (
	"errors"

	"github.com/alibabacloud-go/tea/tea"
)

func IsNotFound(err error) bool {
	var sdkError *tea.SDKError
	if errors.As(err, &sdkError) {
		if *sdkError.StatusCode == 404 {
			return true
		}
	}

	return false
}
