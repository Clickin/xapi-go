package xapi

type Optional[T any] struct {
	Present bool
	Null    bool
	Value   T
}

type Presence uint8

const (
	Missing Presence = iota
	Empty
	ValuePresent
	Null
)
