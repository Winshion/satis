package satis

// Chunk is the parsed representation of a .satis file.
type Chunk struct {
	Meta         map[string]string
	Instructions []Instruction
}

// Instruction is the marker interface for all SatisIL v1 instructions.
type Instruction interface {
	instructionName() string
}

// Value is the minimal SatisIL v1 value node.
//
// V1 only needs two value shapes:
// - string literals, represented with triple brackets in source
// - variable references, represented with @var syntax
type Value struct {
	Kind        ValueKind
	Text        string
	HasSelector bool
	Selector    ListSelector
}

type ListSelector struct {
	HasIndex bool
	Index    int
	HasStart bool
	Start    int
	HasEnd   bool
	End      int
}

type ValueKind string

const (
	ValueKindString   ValueKind = "string"
	ValueKindVariable ValueKind = "variable"
)

type ResolveTargetKind string

const (
	ResolveTargetFile   ResolveTargetKind = "file"
	ResolveTargetFolder ResolveTargetKind = "folder"
)

type ResolveStmt struct {
	Line       int
	TargetKind ResolveTargetKind
	Path       string
	OutputVar  string
}

func (ResolveStmt) instructionName() string { return "resolve" }

type CdStmt struct {
	Line int
	Path string
}

func (CdStmt) instructionName() string { return "cd" }

type PwdStmt struct {
	Line int
}

func (PwdStmt) instructionName() string { return "pwd" }

type LsStmt struct {
	Line int
	Path string
}

func (LsStmt) instructionName() string { return "ls" }

type LoadPwdStmt struct {
	Line int
}

func (LoadPwdStmt) instructionName() string { return "load_pwd" }

type LoadCdStmt struct {
	Line int
	Path string
}

func (LoadCdStmt) instructionName() string { return "load_cd" }

type LoadLsStmt struct {
	Line int
	Path string
}

func (LoadLsStmt) instructionName() string { return "load_ls" }

type LoadStmt struct {
	Line      int
	Sources   []string
	OutputVar string
}

func (LoadStmt) instructionName() string { return "load" }

type CommitStmt struct {
	Line int
}

func (CommitStmt) instructionName() string { return "commit" }

type RollbackStmt struct {
	Line int
}

func (RollbackStmt) instructionName() string { return "rollback" }

type ReadStmt struct {
	Line      int
	ObjectVar string
	Path      string
	StartLine int
	EndLine   int
	OutputVar string
}

func (ReadStmt) instructionName() string { return "read" }

type CreateTargetKind string

const (
	CreateTargetFile   CreateTargetKind = "file"
	CreateTargetFolder CreateTargetKind = "folder"
)

type CreateStmt struct {
	Line       int
	TargetKind CreateTargetKind
	Paths      []string
	HasContent bool
	Content    string
	OutputVar  string
}

func (CreateStmt) instructionName() string { return "create" }

type WriteStmt struct {
	Line      int
	Value     Value
	Path      string
	ObjectVar string
	OutputVar string
}

func (WriteStmt) instructionName() string { return "write" }

type PrintStmt struct {
	Line  int
	Value Value
}

func (PrintStmt) instructionName() string { return "print" }

type ConcatStmt struct {
	Line      int
	Values    []Value
	OutputVar string
}

func (ConcatStmt) instructionName() string { return "concat" }

type CopyStmt struct {
	Line      int
	ObjectVar string
	Path      string
	OutputVar string
}

func (CopyStmt) instructionName() string { return "copy" }

type MoveStmt struct {
	Line      int
	ObjectVar string
	Path      string
	OutputVar string
}

func (MoveStmt) instructionName() string { return "move" }

type PatchStmt struct {
	Line      int
	ObjectVar string
	OldText   string
	NewText   string
	OutputVar string
}

func (PatchStmt) instructionName() string { return "patch" }

type DeleteStmt struct {
	Line              int
	TargetKind        DeleteTargetKind
	Sources           []DeleteSource
	DeleteAll         bool
	DeleteAllKeepRoot bool // true only for "Delete all ." / "Delete all ./" — clear cwd, do not remove the directory itself
}

func (DeleteStmt) instructionName() string { return "delete" }

type DeleteSource struct {
	ObjectVar string
	Path      string
}

type DeleteTargetKind string

const (
	DeleteTargetFile   DeleteTargetKind = "file"
	DeleteTargetFolder DeleteTargetKind = "folder"
)

type RenameStmt struct {
	Line      int
	ObjectVar string
	NewPath   string
	OutputVar string
}

func (RenameStmt) instructionName() string { return "rename" }

type InvokeStmt struct {
	Line            int
	PromptParts     []Value
	Prompt          Value
	ConversationVar string
	HasInput        bool
	Input           Value
	Provider        string
	OutputVar       string
}

func (InvokeStmt) instructionName() string { return "invoke" }

type InvokeProviderStmt struct {
	Line      int
	Action    string
	Name      string
	Flags     []SoftwareFlag
	OutputVar string
}

func (InvokeProviderStmt) instructionName() string { return "invoke_provider" }

// BatchInvokeStmt performs N independent LLM invocations with the same prompt
// over a list of input texts, producing a list of outputs.
//
// Syntax:
//
//	invoke VALUE concurrently with @input_list as @output_prefix mode separate_files
//	invoke VALUE concurrently with @input_list as @output_prefix mode single_file
type BatchInvokeStmt struct {
	Line       int
	Prompt     Value
	InputList  string // variable name holding []string (text values)
	Provider   string
	OutputVar  string // prefix for output variables
	OutputMode string // "separate_files" or "single_file"
}

func (BatchInvokeStmt) instructionName() string { return "invoke_simultaneous" }

type SoftwareManageStmt struct {
	Line      int
	Action    string
	Arg       string
	OutputVar string
}

func (SoftwareManageStmt) instructionName() string { return "software_manage" }

type SoftwareFlag struct {
	Name  string
	Value Value
}

type SoftwareCallStmt struct {
	Line         int
	SoftwareName string
	FunctionName string
	Flags        []SoftwareFlag
	OutputVar    string
}

func (SoftwareCallStmt) instructionName() string { return "software_call" }
