package app

type InputMode string

const (
	ModeLine  InputMode = "line"
	ModeChunk InputMode = "chunk"
)

type State struct {
	Mode       InputMode
	Collecting bool
	Pending    []string
	History    []string
}
