package xapi

import "fmt"

type TypeError struct{ Want, Got string }

func (e *TypeError) Error() string { return fmt.Sprintf("cannot assign %s to %s", e.Got, e.Want) }

type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string { return e.Path + ": " + e.Err.Error() }
func (e *PathError) Unwrap() error { return e.Err }
