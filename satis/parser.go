package satis

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var (
	headerPattern        = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_\-]*)\s*:\s*(.+)$`)
	variablePattern      = regexp.MustCompile(`^@[A-Za-z_][A-Za-z0-9_]*$`)
	valueVariablePattern = regexp.MustCompile(`^(@[A-Za-z_][A-Za-z0-9_]*)(?:\[(-?\d+)?(?::(-?\d+)?)?\])?$`)
	tripleBracketPattern = regexp.MustCompile(`(?s)^\[\[\[(.*)\]\]\]$`)
)

// ParseFile parses a .satis file from disk.
func ParseFile(path string) (*Chunk, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(string(data))
}

type logicalLine struct {
	lineNo int
	text   string
}

// Parse parses a SatisIL v1 chunk from source text.
func Parse(src string) (*Chunk, error) {
	chunk := &Chunk{
		Meta: make(map[string]string),
	}

	lines, err := splitLogicalLines(src)
	if err != nil {
		return nil, err
	}
	seenInstruction := false

	for _, entry := range lines {
		line := strings.TrimSpace(entry.text)
		if line == "" {
			continue
		}

		if !seenInstruction {
			if key, value, ok := parseHeaderLine(line); ok {
				chunk.Meta[key] = value
				continue
			}
		}

		stmt, err := parseInstruction(entry.lineNo, line)
		if err != nil {
			return nil, err
		}
		seenInstruction = true
		chunk.Instructions = append(chunk.Instructions, stmt)
	}

	if len(chunk.Instructions) == 0 {
		return nil, fmt.Errorf("satis parse error: no instructions found")
	}

	return chunk, nil
}

// ParseBody parses one or more SatisIL body instructions without requiring headers.
func ParseBody(src string) ([]Instruction, error) {
	lines, err := splitLogicalLines(src)
	if err != nil {
		return nil, err
	}
	var instructions []Instruction
	for _, entry := range lines {
		line := strings.TrimSpace(entry.text)
		if line == "" {
			continue
		}
		stmt, err := parseInstruction(entry.lineNo, line)
		if err != nil {
			return nil, err
		}
		instructions = append(instructions, stmt)
	}
	if len(instructions) == 0 {
		return nil, fmt.Errorf("satis parse error: no instructions found")
	}
	return instructions, nil
}

func parseHeaderLine(line string) (string, string, bool) {
	match := headerPattern.FindStringSubmatch(line)
	if len(match) != 3 {
		return "", "", false
	}
	return match[1], match[2], true
}

func parseInstruction(lineNo int, line string) (Instruction, error) {
	tokens, err := tokenizeCommandLine(line)
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	if len(tokens) == 0 {
		return nil, parseError(lineNo, "empty instruction")
	}

	switch {
	case equalFold(tokens[0], "cd"):
		if len(tokens) != 2 {
			return nil, parseError(lineNo, "cd requires exactly one path")
		}
		path, err := parsePathToken(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return CdStmt{Line: lineNo, Path: path}, nil

	case equalFold(tokens[0], "pwd"):
		if len(tokens) != 1 {
			return nil, parseError(lineNo, "pwd does not take arguments")
		}
		return PwdStmt{Line: lineNo}, nil

	case equalFold(tokens[0], "ls"):
		if len(tokens) > 2 {
			return nil, parseError(lineNo, "ls accepts at most one path")
		}
		stmt := LsStmt{Line: lineNo}
		if len(tokens) == 2 {
			path, err := parsePathToken(tokens[1])
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
			stmt.Path = path
		}
		return stmt, nil

	case equalFold(tokens[0], "load"):
		return parseLoadInstruction(lineNo, tokens)

	case equalFold(tokens[0], "commit"):
		if len(tokens) != 1 {
			return nil, parseError(lineNo, "commit does not take arguments")
		}
		return CommitStmt{Line: lineNo}, nil

	case equalFold(tokens[0], "rollback"):
		if len(tokens) != 1 {
			return nil, parseError(lineNo, "rollback does not take arguments")
		}
		return RollbackStmt{Line: lineNo}, nil

	case equalFold(tokens[0], "resolve"):
		return parseResolveInstruction(lineNo, tokens)

	case equalFold(tokens[0], "read"):
		return parseReadInstruction(lineNo, tokens)

	case equalFold(tokens[0], "create"):
		return parseCreateInstruction(lineNo, tokens)

	case equalFold(tokens[0], "print"):
		if len(tokens) != 2 {
			return nil, parseError(lineNo, "print requires exactly one value")
		}
		value, err := parseValue(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return PrintStmt{Line: lineNo, Value: value}, nil

	case equalFold(tokens[0], "concat"):
		return parseConcatInstruction(lineNo, tokens)

	case equalFold(tokens[0], "write"):
		return parseWriteInstruction(lineNo, tokens)

	case equalFold(tokens[0], "copy"):
		return parseObjectPathInstruction(lineNo, tokens, "copy")

	case equalFold(tokens[0], "move"):
		return parseObjectPathInstruction(lineNo, tokens, "move")

	case equalFold(tokens[0], "rename"):
		return parseRenameInstruction(lineNo, tokens)

	case equalFold(tokens[0], "delete"):
		if len(tokens) >= 3 && equalFold(tokens[1], "all") {
			sources := make([]DeleteSource, 0, len(tokens)-2)
			keepRoot := len(tokens) == 3
			for _, token := range tokens[2:] {
				source := DeleteSource{}
				if variablePattern.MatchString(token) {
					source.ObjectVar = token
					keepRoot = false
				} else {
					path, err := parsePathToken(token)
					if err != nil {
						return nil, parseError(lineNo, err.Error())
					}
					source.Path = path
					keepRoot = keepRoot && (path == "." || path == "./")
				}
				sources = append(sources, source)
			}
			return DeleteStmt{
				Line:              lineNo,
				TargetKind:        DeleteTargetFolder,
				Sources:           sources,
				DeleteAll:         true,
				DeleteAllKeepRoot: keepRoot,
			}, nil
		}
		if len(tokens) < 3 {
			return nil, parseError(lineNo, "delete syntax is: Delete all SOURCE | Delete file|folder SOURCE [SOURCE...]")
		}
		var targetKind DeleteTargetKind
		switch {
		case equalFold(tokens[1], "file"):
			targetKind = DeleteTargetFile
		case equalFold(tokens[1], "folder"):
			targetKind = DeleteTargetFolder
		default:
			return nil, parseError(lineNo, "delete target must be all, file, or folder")
		}
		if len(tokens[2:]) == 0 {
			return nil, parseError(lineNo, "delete requires at least one source")
		}
		sources := make([]DeleteSource, 0, len(tokens)-2)
		for _, token := range tokens[2:] {
			if variablePattern.MatchString(token) {
				sources = append(sources, DeleteSource{ObjectVar: token})
				continue
			}
			path, err := parsePathToken(token)
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
			sources = append(sources, DeleteSource{Path: path})
		}
		return DeleteStmt{Line: lineNo, TargetKind: targetKind, Sources: sources}, nil

	case equalFold(tokens[0], "patch"):
		return parsePatchInstruction(lineNo, tokens)

	case equalFold(tokens[0], "invoke"):
		return parseInvokeInstruction(lineNo, tokens)

	case equalFold(tokens[0], "software"):
		return parseSoftwareManageInstruction(lineNo, tokens)
	}

	if stmt, ok, err := parseSoftwareCallInstruction(lineNo, tokens); ok {
		return stmt, err
	}

	return nil, parseError(lineNo, "unknown or unsupported instruction")
}

func parseSoftwareManageInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) < 2 {
		return nil, parseError(lineNo, "software requires a subcommand")
	}
	action := strings.ToLower(tokens[1])
	switch action {
	case "ls":
		outputVar := ""
		if len(tokens) == 4 {
			if !equalFold(tokens[2], "as") {
				return nil, parseError(lineNo, "software ls syntax is: Software ls [as @var]")
			}
			var err error
			outputVar, err = parseVariable(tokens[3], "software ls output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		} else if len(tokens) != 2 {
			return nil, parseError(lineNo, "software ls syntax is: Software ls [as @var]")
		}
		return SoftwareManageStmt{Line: lineNo, Action: action, OutputVar: outputVar}, nil
	case "pwd":
		outputVar := ""
		if len(tokens) == 4 {
			if !equalFold(tokens[2], "as") {
				return nil, parseError(lineNo, "software pwd syntax is: Software pwd [as @var]")
			}
			var err error
			outputVar, err = parseVariable(tokens[3], "software pwd output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		} else if len(tokens) != 2 {
			return nil, parseError(lineNo, "software pwd syntax is: Software pwd [as @var]")
		}
		return SoftwareManageStmt{Line: lineNo, Action: action, OutputVar: outputVar}, nil
	case "refresh":
		outputVar := ""
		if len(tokens) == 4 {
			if !equalFold(tokens[2], "as") {
				return nil, parseError(lineNo, "software refresh syntax is: Software refresh [as @var]")
			}
			var err error
			outputVar, err = parseVariable(tokens[3], "software refresh output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		} else if len(tokens) != 2 {
			return nil, parseError(lineNo, "software refresh syntax is: Software refresh [as @var]")
		}
		return SoftwareManageStmt{Line: lineNo, Action: action, OutputVar: outputVar}, nil
	case "cd":
		if len(tokens) != 3 {
			return nil, parseError(lineNo, "software cd syntax is: Software cd PATH")
		}
		return SoftwareManageStmt{Line: lineNo, Action: action, Arg: tokens[2]}, nil
	case "find", "describe", "functions", "readskill":
		if len(tokens) != 3 && len(tokens) != 5 {
			return nil, parseError(lineNo, fmt.Sprintf("software %s syntax is: Software %s ARG [as @var]", action, action))
		}
		stmt := SoftwareManageStmt{Line: lineNo, Action: action, Arg: tokens[2]}
		if len(tokens) == 5 {
			if !equalFold(tokens[3], "as") {
				return nil, parseError(lineNo, fmt.Sprintf("software %s syntax is: Software %s ARG [as @var]", action, action))
			}
			var err error
			stmt.OutputVar, err = parseVariable(tokens[4], fmt.Sprintf("software %s output must be a variable reference", action))
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		}
		return stmt, nil
	default:
		return nil, parseError(lineNo, fmt.Sprintf("unsupported software subcommand %q", tokens[1]))
	}
}

func parseSoftwareCallInstruction(lineNo int, tokens []string) (Instruction, bool, error) {
	if len(tokens) < 2 {
		return nil, false, nil
	}
	reg := DefaultSoftwareRegistry()
	if reg == nil {
		return nil, false, nil
	}
	spec, ok := reg.Lookup(tokens[0])
	if !ok {
		// Treat NAME arg... as an attempted software call so unregistered software
		// surfaces as a clear Satis parse error.
		return nil, true, parseError(lineNo, fmt.Sprintf("unknown software %q", tokens[0]))
	}
	fn, ok := spec.SupportsFunction(tokens[1])
	if !ok {
		return nil, true, parseError(lineNo, fmt.Sprintf("software %q does not support function %q", spec.Name, tokens[1]))
	}
	outputVar := ""
	end := len(tokens)
	if len(tokens) >= 3 && equalFold(tokens[len(tokens)-2], "as") {
		var err error
		outputVar, err = parseVariable(tokens[len(tokens)-1], "software output must be a variable reference")
		if err != nil {
			return nil, true, parseError(lineNo, err.Error())
		}
		end -= 2
	}
	flags := make([]SoftwareFlag, 0, len(tokens))
	seen := map[string]struct{}{}
	i := 2
	for i < end {
		name := tokens[i]
		if !strings.HasPrefix(name, "--") {
			return nil, true, parseError(lineNo, fmt.Sprintf("expected flag starting with --, got %q", name))
		}
		if !fn.AcceptsFlag(name) {
			return nil, true, parseError(lineNo, fmt.Sprintf("function %q of software %q does not accept flag %q", tokens[1], spec.Name, name))
		}
		if _, exists := seen[name]; exists {
			return nil, true, parseError(lineNo, fmt.Sprintf("duplicate flag %q", name))
		}
		if i+1 >= end {
			return nil, true, parseError(lineNo, fmt.Sprintf("missing value for flag %q", name))
		}
		value, err := parseSoftwareArgValue(tokens[i+1])
		if err != nil {
			return nil, true, parseError(lineNo, err.Error())
		}
		seen[name] = struct{}{}
		flags = append(flags, SoftwareFlag{Name: name, Value: value})
		i += 2
	}
	return SoftwareCallStmt{
		Line:         lineNo,
		SoftwareName: spec.Name,
		FunctionName: tokens[1],
		Flags:        flags,
		OutputVar:    outputVar,
	}, true, nil
}

func parseSoftwareArgValue(token string) (Value, error) {
	// Software flags are stringly-typed at runtime.
	// - [[[...]]] is unescaped into plain string
	// - @var remains variable reference for backward compatibility
	// - all other tokens are parsed as raw strings
	if tripleBracketPattern.MatchString(token) {
		text, err := parseTripleBracket(token)
		if err != nil {
			return Value{}, err
		}
		return Value{
			Kind: ValueKindString,
			Text: text,
		}, nil
	}
	if variablePattern.MatchString(token) {
		return Value{
			Kind: ValueKindVariable,
			Text: token,
		}, nil
	}
	return Value{
		Kind: ValueKindString,
		Text: token,
	}, nil
}

func parseLoadInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) < 2 {
		return nil, parseError(lineNo, "load requires a subcommand or source path")
	}
	if len(tokens) == 2 && equalFold(tokens[1], "pwd") {
		return LoadPwdStmt{Line: lineNo}, nil
	}
	if equalFold(tokens[1], "cd") {
		if len(tokens) != 3 {
			return nil, parseError(lineNo, "load cd requires exactly one path")
		}
		path, err := parsePathToken(tokens[2])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return LoadCdStmt{Line: lineNo, Path: path}, nil
	}
	if equalFold(tokens[1], "ls") {
		if len(tokens) > 3 {
			return nil, parseError(lineNo, "load ls accepts at most one path or pattern")
		}
		stmt := LoadLsStmt{Line: lineNo}
		if len(tokens) == 3 {
			path, err := parsePathToken(tokens[2])
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
			stmt.Path = path
		}
		return stmt, nil
	}
	asIdx := -1
	for i := 1; i < len(tokens); i++ {
		if equalFold(tokens[i], "as") {
			asIdx = i
			break
		}
	}
	endSources := len(tokens)
	outputVar := ""
	if asIdx != -1 {
		if asIdx == 1 {
			return nil, parseError(lineNo, "load requires at least one source path")
		}
		if asIdx != len(tokens)-2 {
			return nil, parseError(lineNo, "load output must appear at the end")
		}
		var err error
		outputVar, err = parseVariable(tokens[asIdx+1], "load output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		endSources = asIdx
	}
	sources := make([]string, 0, endSources-1)
	for _, token := range tokens[1:endSources] {
		path, err := parsePathToken(token)
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		sources = append(sources, path)
	}
	if len(sources) == 0 {
		return nil, parseError(lineNo, "load requires at least one source path")
	}
	return LoadStmt{Line: lineNo, Sources: sources, OutputVar: outputVar}, nil
}

func parseResolveInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) < 5 {
		return nil, parseError(lineNo, "resolve requires a target, path, and output variable")
	}
	if len(tokens) != 5 || !equalFold(tokens[3], "as") {
		return nil, parseError(lineNo, "resolve syntax is: Resolve file|folder PATH as @var")
	}
	var kind ResolveTargetKind
	switch {
	case equalFold(tokens[1], "file"):
		kind = ResolveTargetFile
	case equalFold(tokens[1], "folder"):
		kind = ResolveTargetFolder
	default:
		return nil, parseError(lineNo, "resolve target must be file or folder")
	}
	path, err := parsePathToken(tokens[2])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	outputVar, err := parseVariable(tokens[4], "resolve output must be a variable reference")
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	return ResolveStmt{Line: lineNo, TargetKind: kind, Path: path, OutputVar: outputVar}, nil
}

func parseReadInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) == 4 && equalFold(tokens[2], "as") {
		outputVar, err := parseVariable(tokens[3], "read output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		if variablePattern.MatchString(tokens[1]) {
			return ReadStmt{Line: lineNo, ObjectVar: tokens[1], OutputVar: outputVar}, nil
		}
		path, err := parsePathToken(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return ReadStmt{Line: lineNo, Path: path, OutputVar: outputVar}, nil
	}
	if len(tokens) == 7 && equalFold(tokens[2], "lines") && equalFold(tokens[5], "as") {
		start, err := strconv.Atoi(tokens[3])
		if err != nil {
			return nil, parseError(lineNo, "invalid read lines start")
		}
		end, err := strconv.Atoi(tokens[4])
		if err != nil {
			return nil, parseError(lineNo, "invalid read lines end")
		}
		if start <= 0 || end <= 0 || start > end {
			return nil, parseError(lineNo, "invalid read lines range")
		}
		outputVar, err := parseVariable(tokens[6], "read output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt := ReadStmt{
			Line:      lineNo,
			StartLine: start,
			EndLine:   end,
			OutputVar: outputVar,
		}
		if variablePattern.MatchString(tokens[1]) {
			stmt.ObjectVar = tokens[1]
			return stmt, nil
		}
		path, err := parsePathToken(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.Path = path
		return stmt, nil
	}
	return nil, parseError(lineNo, "read syntax is: Read @obj as @var, Read PATH as @var, Read @obj lines N M as @var, or Read PATH lines N M as @var")
}

func parseConcatInstruction(lineNo int, tokens []string) (Instruction, error) {
	asIdx, err := findAsIndex(tokens, 1)
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	if asIdx == 1 {
		return nil, parseError(lineNo, "concat requires at least one value")
	}
	outputVar, err := parseOutputVar(tokens, asIdx)
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	values, err := parseValueList(tokens[1:asIdx])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	return ConcatStmt{Line: lineNo, Values: values, OutputVar: outputVar}, nil
}

func parseWriteInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) != 4 && len(tokens) != 6 {
		return nil, parseError(lineNo, "write syntax is: Write VALUE into PATH|@file [as @var]")
	}
	if !equalFold(tokens[2], "into") {
		return nil, parseError(lineNo, "write syntax is: Write VALUE into PATH|@file [as @var]")
	}
	value, err := parseValue(tokens[1])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	path, objectVar, err := parseWriteTargetToken(tokens[3])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	outputVar := ""
	if len(tokens) == 6 {
		if !equalFold(tokens[4], "as") {
			return nil, parseError(lineNo, "write syntax is: Write VALUE into PATH|@file [as @var]")
		}
		outputVar, err = parseVariable(tokens[5], "write output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
	}
	return WriteStmt{Line: lineNo, Value: value, Path: path, ObjectVar: objectVar, OutputVar: outputVar}, nil
}

func parseWriteTargetToken(token string) (string, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", "", fmt.Errorf("expected path")
	}
	if variablePattern.MatchString(token) {
		return "", token, nil
	}
	path, err := parsePathToken(token)
	if err != nil {
		return "", "", err
	}
	return path, "", nil
}

func parseCreateInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) < 3 {
		return nil, parseError(lineNo, "create syntax is: Create file PATH [PATH ...] [with [[[text]]]] [as @var] or Create folder PATH [PATH ...] [as @var]")
	}
	var targetKind CreateTargetKind
	switch {
	case equalFold(tokens[1], "file"):
		targetKind = CreateTargetFile
	case equalFold(tokens[1], "folder"):
		targetKind = CreateTargetFolder
	default:
		return nil, parseError(lineNo, "create target must be file or folder")
	}

	stmt := CreateStmt{
		Line:       lineNo,
		TargetKind: targetKind,
	}
	asIdx := -1
	withIdx := -1
	for i := 2; i < len(tokens); i++ {
		switch {
		case equalFold(tokens[i], "with"):
			if withIdx != -1 {
				return nil, parseError(lineNo, "create allows at most one with clause")
			}
			withIdx = i
		case equalFold(tokens[i], "as"):
			if asIdx != -1 {
				return nil, parseError(lineNo, "create allows at most one as clause")
			}
			asIdx = i
		}
	}

	endPaths := len(tokens)
	if withIdx != -1 {
		endPaths = withIdx
	} else if asIdx != -1 {
		endPaths = asIdx
	}
	if endPaths <= 2 {
		return nil, parseError(lineNo, "create requires at least one path")
	}
	stmt.Paths = make([]string, 0, endPaths-2)
	for _, token := range tokens[2:endPaths] {
		path, err := parsePathToken(token)
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.Paths = append(stmt.Paths, path)
	}

	if withIdx != -1 {
		if targetKind != CreateTargetFile {
			return nil, parseError(lineNo, "create folder does not support with [[[text]]]")
		}
		if asIdx != -1 {
			if withIdx+2 != asIdx {
				return nil, parseError(lineNo, "create file syntax is: Create file PATH [PATH ...] [with [[[text]]]] [as @var]")
			}
		} else if withIdx+2 != len(tokens) {
			return nil, parseError(lineNo, "create file syntax is: Create file PATH [PATH ...] [with [[[text]]]] [as @var]")
		}
		content, err := parseTripleBracket(tokens[withIdx+1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.HasContent = true
		stmt.Content = content
	}

	if asIdx != -1 {
		if asIdx+1 != len(tokens)-1 {
			return nil, parseError(lineNo, "create output must appear at the end")
		}
		outputVar, err := parseVariable(tokens[asIdx+1], "create output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.OutputVar = outputVar
	}

	return stmt, nil
}

func parseObjectPathInstruction(lineNo int, tokens []string, kind string) (Instruction, error) {
	if len(tokens) != 4 && len(tokens) != 6 {
		return nil, parseError(lineNo, fmt.Sprintf("%s syntax is: %s @obj to PATH [as @var]", kind, strings.Title(kind)))
	}
	if !variablePattern.MatchString(tokens[1]) || !equalFold(tokens[2], "to") {
		return nil, parseError(lineNo, fmt.Sprintf("%s syntax is: %s @obj to PATH [as @var]", kind, strings.Title(kind)))
	}
	path, err := parsePathToken(tokens[3])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	outputVar := ""
	if len(tokens) == 6 {
		if !equalFold(tokens[4], "as") {
			return nil, parseError(lineNo, fmt.Sprintf("%s syntax is: %s @obj to PATH [as @var]", kind, strings.Title(kind)))
		}
		outputVar, err = parseVariable(tokens[5], kind+" output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
	}
	switch kind {
	case "copy":
		return CopyStmt{Line: lineNo, ObjectVar: tokens[1], Path: path, OutputVar: outputVar}, nil
	case "move":
		return MoveStmt{Line: lineNo, ObjectVar: tokens[1], Path: path, OutputVar: outputVar}, nil
	default:
		return nil, parseError(lineNo, "unsupported object-path instruction")
	}
}

func parseRenameInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) != 4 && len(tokens) != 6 {
		return nil, parseError(lineNo, "rename syntax is: Rename @obj to PATH [as @var]")
	}
	if !variablePattern.MatchString(tokens[1]) || !equalFold(tokens[2], "to") {
		return nil, parseError(lineNo, "rename syntax is: Rename @obj to PATH [as @var]")
	}
	path, err := parsePathToken(tokens[3])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	outputVar := ""
	if len(tokens) == 6 {
		if !equalFold(tokens[4], "as") {
			return nil, parseError(lineNo, "rename syntax is: Rename @obj to PATH [as @var]")
		}
		outputVar, err = parseVariable(tokens[5], "rename output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
	}
	return RenameStmt{Line: lineNo, ObjectVar: tokens[1], NewPath: path, OutputVar: outputVar}, nil
}

func parsePatchInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) != 6 && len(tokens) != 8 {
		return nil, parseError(lineNo, "patch syntax is: Patch @obj replace [[[old]]] with [[[new]]] [as @var]")
	}
	if !variablePattern.MatchString(tokens[1]) || !equalFold(tokens[2], "replace") || !equalFold(tokens[4], "with") {
		return nil, parseError(lineNo, "patch syntax is: Patch @obj replace [[[old]]] with [[[new]]] [as @var]")
	}
	oldText, err := parseTripleBracket(tokens[3])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	newText, err := parseTripleBracket(tokens[5])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	outputVar := ""
	if len(tokens) == 8 {
		if !equalFold(tokens[6], "as") {
			return nil, parseError(lineNo, "patch syntax is: Patch @obj replace [[[old]]] with [[[new]]] [as @var]")
		}
		outputVar, err = parseVariable(tokens[7], "patch output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
	}
	return PatchStmt{Line: lineNo, ObjectVar: tokens[1], OldText: oldText, NewText: newText, OutputVar: outputVar}, nil
}

func parseInvokeInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) >= 2 && equalFold(tokens[1], "provider") {
		return parseInvokeProviderInstruction(lineNo, tokens)
	}
	if len(tokens) == 7 && equalFold(tokens[2], "concurrently") && equalFold(tokens[3], "with") && equalFold(tokens[5], "as") {
		prompt, err := parseValue(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		inputVar, err := parseVariable(tokens[4], "concurrent invoke input must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		outputVar, err := parseVariable(tokens[6], "concurrent invoke output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return BatchInvokeStmt{Line: lineNo, Prompt: prompt, InputList: inputVar, OutputVar: outputVar, OutputMode: "separate_files"}, nil
	}
	if len(tokens) == 9 && equalFold(tokens[2], "concurrently") && equalFold(tokens[3], "with") && equalFold(tokens[5], "as") && equalFold(tokens[7], "mode") {
		prompt, err := parseValue(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		inputVar, err := parseVariable(tokens[4], "concurrent invoke input must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		outputVar, err := parseVariable(tokens[6], "concurrent invoke output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		mode := tokens[8]
		return BatchInvokeStmt{Line: lineNo, Prompt: prompt, InputList: inputVar, OutputVar: outputVar, OutputMode: mode}, nil
	}
	if len(tokens) == 9 && equalFold(tokens[2], "concurrently") && equalFold(tokens[3], "with") && equalFold(tokens[5], "via") && equalFold(tokens[7], "as") {
		prompt, err := parseValue(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		inputVar, err := parseVariable(tokens[4], "concurrent invoke input must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		outputVar, err := parseVariable(tokens[8], "concurrent invoke output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return BatchInvokeStmt{Line: lineNo, Prompt: prompt, InputList: inputVar, Provider: tokens[6], OutputVar: outputVar, OutputMode: "separate_files"}, nil
	}
	if len(tokens) == 11 && equalFold(tokens[2], "concurrently") && equalFold(tokens[3], "with") && equalFold(tokens[5], "via") && equalFold(tokens[7], "as") && equalFold(tokens[9], "mode") {
		prompt, err := parseValue(tokens[1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		inputVar, err := parseVariable(tokens[4], "concurrent invoke input must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		outputVar, err := parseVariable(tokens[8], "concurrent invoke output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		return BatchInvokeStmt{Line: lineNo, Prompt: prompt, InputList: inputVar, Provider: tokens[6], OutputVar: outputVar, OutputMode: tokens[10]}, nil
	}
	if len(tokens) < 2 {
		return nil, parseError(lineNo, "invoke requires a prompt")
	}

	promptEnd := 1
	for promptEnd < len(tokens) {
		if equalFold(tokens[promptEnd], "in") || equalFold(tokens[promptEnd], "with") || equalFold(tokens[promptEnd], "via") || equalFold(tokens[promptEnd], "as") {
			break
		}
		promptEnd++
	}
	if promptEnd == 1 {
		return nil, parseError(lineNo, "invoke requires at least one prompt value")
	}
	promptParts, err := parseValueList(tokens[1:promptEnd])
	if err != nil {
		return nil, parseError(lineNo, err.Error())
	}
	stmt := InvokeStmt{
		Line:        lineNo,
		PromptParts: promptParts,
		Prompt:      promptParts[0],
	}

	idx := promptEnd
	if idx < len(tokens) && equalFold(tokens[idx], "in") {
		if idx+1 >= len(tokens) {
			return nil, parseError(lineNo, "invoke conversation must be a variable reference")
		}
		conversationVar, err := parseVariable(tokens[idx+1], "invoke conversation must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.ConversationVar = conversationVar
		idx += 2
	}
	if idx < len(tokens) && equalFold(tokens[idx], "with") {
		if idx+1 >= len(tokens) {
			return nil, parseError(lineNo, "invoke with requires a value")
		}
		input, err := parseValue(tokens[idx+1])
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.HasInput = true
		stmt.Input = input
		idx += 2
	}
	if idx < len(tokens) && equalFold(tokens[idx], "via") {
		if idx+1 >= len(tokens) {
			return nil, parseError(lineNo, "invoke via requires a provider name")
		}
		stmt.Provider = tokens[idx+1]
		idx += 2
	}
	if idx < len(tokens) && equalFold(tokens[idx], "as") {
		if idx+1 >= len(tokens) {
			return nil, parseError(lineNo, "invoke output must be a variable reference")
		}
		outputVar, err := parseVariable(tokens[idx+1], "invoke output must be a variable reference")
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.OutputVar = outputVar
		idx += 2
	}
	if idx != len(tokens) {
		return nil, parseError(lineNo, "invoke syntax: Invoke VALUE [VALUE ...] [in @conversation] [with VALUE] [via PROVIDER] [as @var] | Invoke VALUE concurrently with @list [via PROVIDER] as @var [mode separate_files|single_file]")
	}
	return stmt, nil
}

func parseInvokeProviderInstruction(lineNo int, tokens []string) (Instruction, error) {
	if len(tokens) < 3 {
		return nil, parseError(lineNo, "invoke provider requires a subcommand")
	}
	action := strings.ToLower(tokens[2])
	switch action {
	case "ls", "current":
		outputVar := ""
		if len(tokens) == 5 {
			if !equalFold(tokens[3], "as") {
				return nil, parseError(lineNo, fmt.Sprintf("invoke provider %s syntax is: Invoke provider %s [as @var]", action, action))
			}
			var err error
			outputVar, err = parseVariable(tokens[4], "invoke provider output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		} else if len(tokens) != 3 {
			return nil, parseError(lineNo, fmt.Sprintf("invoke provider %s syntax is: Invoke provider %s [as @var]", action, action))
		}
		return InvokeProviderStmt{Line: lineNo, Action: action, OutputVar: outputVar}, nil
	case "show", "remove", "set-default":
		if len(tokens) != 4 && len(tokens) != 6 {
			return nil, parseError(lineNo, fmt.Sprintf("invoke provider %s syntax is: Invoke provider %s NAME [as @var]", action, action))
		}
		stmt := InvokeProviderStmt{Line: lineNo, Action: action, Name: tokens[3]}
		if len(tokens) == 6 {
			if !equalFold(tokens[4], "as") {
				return nil, parseError(lineNo, fmt.Sprintf("invoke provider %s syntax is: Invoke provider %s NAME [as @var]", action, action))
			}
			var err error
			stmt.OutputVar, err = parseVariable(tokens[5], "invoke provider output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
		}
		return stmt, nil
	case "upsert":
		if len(tokens) < 5 {
			return nil, parseError(lineNo, "invoke provider upsert syntax is: Invoke provider upsert NAME --base-url VALUE --model VALUE [--api-key VALUE] [--api-key-env VALUE] [--timeout-seconds VALUE] [--temperature VALUE] [--max-tokens VALUE] [as @var]")
		}
		stmt := InvokeProviderStmt{Line: lineNo, Action: action, Name: tokens[3]}
		end := len(tokens)
		if len(tokens) >= 2 && equalFold(tokens[len(tokens)-2], "as") {
			var err error
			stmt.OutputVar, err = parseVariable(tokens[len(tokens)-1], "invoke provider output must be a variable reference")
			if err != nil {
				return nil, parseError(lineNo, err.Error())
			}
			end -= 2
		}
		flags, err := parseNamedFlags(tokens[4:end], nil)
		if err != nil {
			return nil, parseError(lineNo, err.Error())
		}
		stmt.Flags = flags
		return stmt, nil
	default:
		return nil, parseError(lineNo, fmt.Sprintf("unsupported invoke provider subcommand %q", tokens[2]))
	}
}

func parseNamedFlags(tokens []string, allow map[string]struct{}) ([]SoftwareFlag, error) {
	flags := make([]SoftwareFlag, 0, len(tokens)/2)
	seen := map[string]struct{}{}
	for i := 0; i < len(tokens); {
		name := tokens[i]
		if !strings.HasPrefix(name, "--") {
			return nil, fmt.Errorf("expected flag starting with --, got %q", name)
		}
		if allow != nil {
			if _, ok := allow[name]; !ok {
				return nil, fmt.Errorf("unsupported flag %q", name)
			}
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate flag %q", name)
		}
		if i+1 >= len(tokens) {
			return nil, fmt.Errorf("missing value for flag %q", name)
		}
		value, err := parseSoftwareArgValue(tokens[i+1])
		if err != nil {
			return nil, err
		}
		flags = append(flags, SoftwareFlag{Name: name, Value: value})
		seen[name] = struct{}{}
		i += 2
	}
	return flags, nil
}

func parseValue(token string) (Value, error) {
	if match := valueVariablePattern.FindStringSubmatch(token); len(match) > 0 {
		value := Value{
			Kind: ValueKindVariable,
			Text: match[1],
		}
		if strings.Contains(token, "[") {
			selector, err := parseListSelector(token, match)
			if err != nil {
				return Value{}, err
			}
			value.HasSelector = true
			value.Selector = selector
		}
		return value, nil
	}

	if tripleBracketPattern.MatchString(token) {
		value, err := parseTripleBracket(token)
		if err != nil {
			return Value{}, err
		}
		return Value{
			Kind: ValueKindString,
			Text: value,
		}, nil
	}

	return Value{}, fmt.Errorf("invalid value %q", token)
}

func parseListSelector(token string, match []string) (ListSelector, error) {
	if len(match) < 4 {
		return ListSelector{}, fmt.Errorf("invalid selector %q", token)
	}
	selectorTextStart := strings.Index(token, "[")
	selectorTextEnd := strings.LastIndex(token, "]")
	if selectorTextStart < 0 || selectorTextEnd < selectorTextStart {
		return ListSelector{}, fmt.Errorf("invalid selector %q", token)
	}
	selectorText := token[selectorTextStart+1 : selectorTextEnd]
	if !strings.Contains(selectorText, ":") {
		if match[2] == "" {
			return ListSelector{}, fmt.Errorf("invalid selector %q", token)
		}
		index, err := strconv.Atoi(match[2])
		if err != nil {
			return ListSelector{}, fmt.Errorf("invalid selector %q", token)
		}
		return ListSelector{HasIndex: true, Index: index}, nil
	}
	selector := ListSelector{}
	if match[2] != "" {
		start, err := strconv.Atoi(match[2])
		if err != nil {
			return ListSelector{}, fmt.Errorf("invalid selector %q", token)
		}
		selector.HasStart = true
		selector.Start = start
	}
	if match[3] != "" {
		end, err := strconv.Atoi(match[3])
		if err != nil {
			return ListSelector{}, fmt.Errorf("invalid selector %q", token)
		}
		selector.HasEnd = true
		selector.End = end
	}
	return selector, nil
}

func parseValueList(tokens []string) ([]Value, error) {
	values := make([]Value, 0, len(tokens))
	for _, token := range tokens {
		value, err := parseValue(token)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func isSpace(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r':
		return true
	default:
		return false
	}
}

func builderIsBlank(b *strings.Builder) bool {
	if b == nil || b.Len() == 0 {
		return true
	}
	return strings.TrimSpace(b.String()) == ""
}

func parseTripleBracket(token string) (string, error) {
	match := tripleBracketPattern.FindStringSubmatch(token)
	if len(match) != 2 {
		return "", fmt.Errorf("expected triple-bracket string")
	}
	return unescapeTripleBracket(match[1])
}

func unescapeTripleBracket(raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(raw) {
			return "", fmt.Errorf("unterminated escape sequence")
		}
		i++
		switch raw[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case '\\':
			b.WriteByte('\\')
		default:
			return "", fmt.Errorf("unsupported escape sequence \\%c", raw[i])
		}
	}

	return b.String(), nil
}

func parseError(line int, msg string) error {
	return fmt.Errorf("satis parse error on line %d: %s", line, msg)
}

func parseVariable(token string, message string) (string, error) {
	if !variablePattern.MatchString(token) {
		return "", fmt.Errorf("%s", message)
	}
	return token, nil
}

func parsePathToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("expected path")
	}
	if strings.HasPrefix(token, "[[[") {
		return "", fmt.Errorf("paths must use bare or shell-quoted syntax in v1")
	}
	if variablePattern.MatchString(token) {
		return "", fmt.Errorf("paths cannot be variable references")
	}
	return token, nil
}

func findAsIndex(tokens []string, start int) (int, error) {
	for i := start; i < len(tokens); i++ {
		if equalFold(tokens[i], "as") {
			if i != len(tokens)-2 {
				return 0, fmt.Errorf("as must be followed by exactly one output variable")
			}
			return i, nil
		}
	}
	return 0, fmt.Errorf("missing as clause")
}

func parseOutputVar(tokens []string, asIdx int) (string, error) {
	if asIdx < 0 || asIdx+1 >= len(tokens) {
		return "", fmt.Errorf("missing output variable")
	}
	return parseVariable(tokens[asIdx+1], "output must be a variable reference")
}

func equalFold(left, right string) bool {
	return strings.EqualFold(left, right)
}

func tokenizeCommandLine(src string) ([]string, error) {
	var tokens []string
	for i := 0; i < len(src); {
		for i < len(src) && isSpace(src[i]) {
			i++
		}
		if i >= len(src) {
			break
		}
		switch {
		case strings.HasPrefix(src[i:], "[[["):
			end := i + 3
			for end < len(src) && !strings.HasPrefix(src[end:], "]]]") {
				end++
			}
			if end >= len(src) {
				return nil, fmt.Errorf("unterminated triple-bracket string")
			}
			tokens = append(tokens, src[i:end+3])
			i = end + 3

		case src[i] == '\'':
			token, next, err := scanSingleQuoted(src, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token)
			i = next

		case src[i] == '"':
			token, next, err := scanDoubleQuoted(src, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token)
			i = next

		default:
			token, next, err := scanBareToken(src, i)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, token)
			i = next
		}
	}
	return tokens, nil
}

func scanSingleQuoted(src string, start int) (string, int, error) {
	var b strings.Builder
	for i := start + 1; i < len(src); i++ {
		if src[i] == '\'' {
			return b.String(), i + 1, nil
		}
		b.WriteByte(src[i])
	}
	return "", 0, fmt.Errorf("unterminated single-quoted token")
}

func scanDoubleQuoted(src string, start int) (string, int, error) {
	var b strings.Builder
	for i := start + 1; i < len(src); i++ {
		switch src[i] {
		case '"':
			return b.String(), i + 1, nil
		case '\\':
			if i+1 >= len(src) {
				return "", 0, fmt.Errorf("unterminated escape in double-quoted token")
			}
			i++
			b.WriteByte(src[i])
		default:
			b.WriteByte(src[i])
		}
	}
	return "", 0, fmt.Errorf("unterminated double-quoted token")
}

func scanBareToken(src string, start int) (string, int, error) {
	var b strings.Builder
	for i := start; i < len(src); i++ {
		if isSpace(src[i]) {
			return b.String(), i, nil
		}
		if src[i] == '\\' {
			if i+1 >= len(src) {
				return "", 0, fmt.Errorf("unterminated escape in token")
			}
			i++
			b.WriteByte(src[i])
			continue
		}
		b.WriteByte(src[i])
	}
	return b.String(), len(src), nil
}

func splitLogicalLines(src string) ([]logicalLine, error) {
	var (
		lines            []logicalLine
		current          strings.Builder
		lineNo           = 1
		logicalStartLine = 1
		inTriple         bool
		inComment        bool
	)

	for i := 0; i < len(src); {
		switch {
		case inComment:
			if strings.HasPrefix(src[i:], "*/") {
				inComment = false
				i += 2
				continue
			}
			if src[i] == '\n' {
				lineNo++
			}
			i++
		case inTriple:
			if strings.HasPrefix(src[i:], "]]]") {
				current.WriteString("]]]")
				inTriple = false
				i += 3
				continue
			}
			if src[i] == '\n' {
				current.WriteByte('\n')
				lineNo++
				i++
				continue
			}
			current.WriteByte(src[i])
			i++
		default:
			if strings.HasPrefix(src[i:], "/*") && builderIsBlank(&current) {
				inComment = true
				i += 2
				continue
			}
			if strings.HasPrefix(src[i:], "[[[") {
				if current.Len() == 0 {
					logicalStartLine = lineNo
				}
				current.WriteString("[[[")
				inTriple = true
				i += 3
				continue
			}
			if src[i] == '\r' {
				i++
				continue
			}
			if src[i] == '\n' {
				lines = append(lines, logicalLine{
					lineNo: logicalStartLine,
					text:   current.String(),
				})
				current.Reset()
				lineNo++
				logicalStartLine = lineNo
				i++
				continue
			}
			if current.Len() == 0 {
				logicalStartLine = lineNo
			}
			current.WriteByte(src[i])
			i++
		}
	}

	if inComment {
		return nil, fmt.Errorf("satis parse error on line %d: unterminated block comment", lineNo)
	}
	if inTriple {
		return nil, fmt.Errorf("satis parse error on line %d: unterminated triple-bracket string", logicalStartLine)
	}
	if current.Len() > 0 || len(lines) == 0 {
		lines = append(lines, logicalLine{
			lineNo: logicalStartLine,
			text:   current.String(),
		})
	}

	return lines, nil
}
