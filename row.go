package xapi

type RowType uint8

const (
	RowNormal RowType = iota
	RowInsert
	RowUpdate
	RowDelete
)

type Tracked[T any] struct {
	Type     RowType
	Current  T
	Original *T
}
