package mcprelay

import "fmt"

type RPCError struct {
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("downstream rpc error (%d): %s", e.Code, e.Message)
}
