package stack

import "errors"

type terminalSize struct {
	Cols uint16
	Rows uint16
}

var errPTYUnsupported = errors.New("interactive console is not supported on this worker platform")
