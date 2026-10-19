package atomicfile

import (
	"errors"
	"syscall"
)

func isEINVAL(err error) bool { return errors.Is(err, syscall.EINVAL) }
