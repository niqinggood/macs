package processutil

import (
	"context"
	"io"
)

// PTYProcess is the cross-platform contract used by interactive model sessions.
type PTYProcess interface {
	io.Reader
	io.Writer
	Close() error
	Wait(context.Context) error
}
