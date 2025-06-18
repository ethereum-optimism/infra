package proxyd

import (
	"errors"
	"fmt"
)

var ErrTooManyRequests = errors.New("too many requests")

func wrapErr(err error, msg string) error {
	return fmt.Errorf("%s\n%w", msg, err)
}
