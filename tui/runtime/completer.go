package tuiruntime

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/chzyer/readline"

	"satis/satis"
	"satis/vfs"
)

var _ readline.AutoCompleter = (*ReadlineCompleter)(nil)

var tuiCommandNames = []string{
	"/help",
	"/history",
	"/exit",
	"/clear",
	"/clearchunk",
	"/mode",
	"/begin",
	"/commit",
	"/cancel",
	"/exec",
	"/workbench",
	"/plan-continue",
	"/plan-draft",
	"/plan-finish",
}

var satisInstructionNames = []string{
	"Cd",
	"Pwd",
	"Ls",
	"Commit",
	"Rollback",
	"Resolve",
	"Read",
	"Create",
	"Print",
	"Concat",
	"Write",
	"Copy",
	"Move",
	"Rename",
	"Delete",
	"Patch",
	"Invoke",
	"Load",
	"Software",
}

type ReadlineCompleter struct {
	adapter *SessionAdapter
}

type variableCompletionMode int

const (
	variableCompletionRuntime variableCompletionMode = iota
	variableCompletionTextOnly
	variableCompletionTextOrList
	variableCompletionObjectOnly
	variableCompletionConversation
)

func NewReadlineCompleter(adapter *SessionAdapter) *ReadlineCompleter {
	return &ReadlineCompleter{adapter: adapter}
}

func (c *ReadlineCompleter) Do(line []rune, pos int) ([][]rune, int) {
	if pos < 0 {
		pos = 0
	}
	if pos > len(line) {
		pos = len(line)
	}
	prefix := string(line[:pos])
	tokenStart := tokenStartIndex(line, pos)
	currentToken := string(line[tokenStart:pos])
	beforeToken := strings.TrimRightFunc(string(line[:tokenStart]), unicode.IsSpace)
	fieldsBefore := strings.Fields(beforeToken)

	candidates := c.complete(prefix, currentToken, fieldsBefore)
	if len(candidates) == 0 {
		return nil, 0
	}

	out := make([][]rune, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, []rune(candidate))
	}
	// readline appends candidate runes at the cursor without deleting the partial token.
	// Candidates must be suffixes after currentToken; offset is len(current token) for UI aggregation.
	return out, pos - tokenStart
}

func (c *ReadlineCompleter) complete(prefix string, currentToken string, fieldsBefore []string) []string {
	if tokenStartIsCommand(prefix, fieldsBefore) {
		return replaceCandidates(currentToken, filterByPrefix(tuiCommandNames, currentToken))
	}
	if len(fieldsBefore) > 0 && strings.HasPrefix(fieldsBefore[0], "/") {
		return c.completeTUICommand(currentToken, fieldsBefore)
	}
	if len(fieldsBefore) == 0 {
		base := replaceCandidates(currentToken, filterByPrefix(satisInstructionNames, currentToken))
		reg := satis.DefaultSoftwareRegistry()
		if reg == nil {
			return base
		}
		return mergeCandidates(base, replaceCandidates(currentToken, filterByPrefix(reg.Names(), currentToken)))
	}
	return c.completeInstruction(currentToken, fieldsBefore)
}

func tokenStartIsCommand(prefix string, fieldsBefore []string) bool {
	trimmed := strings.TrimLeftFunc(prefix, unicode.IsSpace)
	return len(fieldsBefore) == 0 && strings.HasPrefix(trimmed, "/")
}

func (c *ReadlineCompleter) completeTUICommand(currentToken string, fieldsBefore []string) []string {
	cmdName := strings.TrimPrefix(strings.ToLower(fieldsBefore[0]), "/")
	switch cmdName {
	case "mode":
		return replaceCandidates(currentToken, filterByPrefix([]string{"line", "chunk"}, currentToken))
	case "exec":
		return c.completeHostPath(currentToken)
	case "workbench":
		return c.completeVirtualPath(currentToken)
	default:
		return nil
	}
}

func (c *ReadlineCompleter) completeInstruction(currentToken string, fieldsBefore []string) []string {
	if len(fieldsBefore) == 0 {
		return nil
	}
	if suggestions := c.completeSoftwareInstruction(currentToken, fieldsBefore); len(suggestions) > 0 {
		return suggestions
	}
	if mode, ok := variableCompletionContext(currentToken, fieldsBefore); ok {
		return c.completeVariable(currentToken, mode)
	}
	switch strings.ToLower(fieldsBefore[0]) {
	case "load":
		return c.completeLoadInstruction(currentToken, fieldsBefore)
	case "software":
		return c.completeSoftwareManage(currentToken, fieldsBefore)
	case "delete":
		if len(fieldsBefore) == 1 {
			return replaceCandidates(currentToken, filterByPrefix([]string{"all", "file", "folder"}, currentToken))
		}
	case "resolve":
		if len(fieldsBefore) == 1 {
			return replaceCandidates(currentToken, filterByPrefix([]string{"file", "folder"}, currentToken))
		}
	}
	if !isVirtualPathContext(fieldsBefore) {
		return nil
	}
	return c.completeVirtualPath(currentToken)
}

func (c *ReadlineCompleter) completeSoftwareManage(currentToken string, fieldsBefore []string) []string {
	subcommands := []string{"ls", "pwd", "cd", "find", "describe", "functions", "readskill", "refresh"}
	if len(fieldsBefore) == 1 {
		return replaceCandidates(currentToken, filterByPrefix(subcommands, currentToken))
	}
	action := strings.ToLower(fieldsBefore[1])
	last := strings.ToLower(fieldsBefore[len(fieldsBefore)-1])
	switch action {
	case "ls", "pwd", "refresh":
		if last == "as" {
			return c.completeVariable(currentToken, variableCompletionRuntime)
		}
		if len(fieldsBefore) == 2 {
			return replaceCandidates(currentToken, filterByPrefix([]string{"as"}, currentToken))
		}
		return nil
	case "cd":
		return c.completeSoftwareRegistryPath(currentToken)
	case "find", "describe", "functions", "readskill":
		if last == "as" {
			return c.completeVariable(currentToken, variableCompletionRuntime)
		}
		if len(fieldsBefore) == 2 {
			return c.completeSoftwareName(currentToken)
		}
		if len(fieldsBefore) == 3 {
			return replaceCandidates(currentToken, filterByPrefix([]string{"as"}, currentToken))
		}
		return nil
	}
	return nil
}

func (c *ReadlineCompleter) completeSoftwareName(currentToken string) []string {
	reg := satis.DefaultSoftwareRegistry()
	if reg == nil {
		return nil
	}
	return replaceCandidates(currentToken, filterByPrefix(reg.Names(), currentToken))
}

func (c *ReadlineCompleter) completeSoftwareInstruction(currentToken string, fieldsBefore []string) []string {
	reg := satis.DefaultSoftwareRegistry()
	if reg == nil {
		return nil
	}
	if len(fieldsBefore) == 0 {
		return nil
	}
	spec, ok := reg.Lookup(fieldsBefore[0])
	if !ok {
		return nil
	}
	if len(fieldsBefore) == 1 {
		functions := make([]string, 0, len(spec.Functions))
		for name := range spec.Functions {
			functions = append(functions, name)
		}
		sort.Strings(functions)
		return replaceCandidates(currentToken, filterByPrefix(functions, currentToken))
	}
	fn, ok := spec.SupportsFunction(fieldsBefore[1])
	if !ok {
		return nil
	}
	state := analyzeSoftwareCallCompletion(fieldsBefore[2:])
	if state.expectRuntimeVar || strings.EqualFold(fieldsBefore[len(fieldsBefore)-1], "as") {
		return c.completeVariable(currentToken, variableCompletionRuntime)
	}
	if state.expectFlagValue {
		return c.completeVariable(currentToken, variableCompletionTextOnly)
	}
	if strings.HasPrefix(currentToken, "@") {
		return c.completeVariable(currentToken, variableCompletionTextOnly)
	}
	if strings.HasPrefix(currentToken, "--") || currentToken == "" {
		candidates := softwareFlagCandidates(fn.Args, state.usedFlags)
		if !state.expectFlagValue {
			candidates = append(candidates, "as")
		}
		return replaceCandidates(currentToken, filterByPrefix(candidates, currentToken))
	}
	return replaceCandidates(currentToken, filterByPrefix(softwareFlagCandidates(fn.Args, state.usedFlags), currentToken))
}

type softwareCallCompletionState struct {
	usedFlags        map[string]struct{}
	expectFlagValue  bool
	expectRuntimeVar bool
}

func analyzeSoftwareCallCompletion(tokens []string) softwareCallCompletionState {
	state := softwareCallCompletionState{usedFlags: make(map[string]struct{})}
	expectValueForFlag := false
	for _, token := range tokens {
		if expectValueForFlag {
			expectValueForFlag = false
			continue
		}
		if strings.EqualFold(token, "as") {
			state.expectRuntimeVar = true
			return state
		}
		if strings.HasPrefix(token, "--") {
			state.usedFlags[token] = struct{}{}
			expectValueForFlag = true
		}
	}
	state.expectFlagValue = expectValueForFlag
	return state
}

func softwareFlagCandidates(args []string, used map[string]struct{}) []string {
	if len(args) == 0 {
		return nil
	}
	candidates := make([]string, 0, len(args))
	for _, arg := range args {
		if _, ok := used[arg]; ok {
			continue
		}
		candidates = append(candidates, arg)
	}
	sort.Strings(candidates)
	return candidates
}

func (c *ReadlineCompleter) completeLoadInstruction(currentToken string, fieldsBefore []string) []string {
	subcommands := replaceCandidates(currentToken, filterByPrefix([]string{"pwd", "ls", "cd"}, currentToken))
	if len(fieldsBefore) == 1 {
		return mergeCandidates(subcommands, c.completeLoadPath(currentToken))
	}

	switch strings.ToLower(fieldsBefore[1]) {
	case "pwd":
		return nil
	case "cd", "ls":
		return c.completeLoadPath(currentToken)
	default:
		for _, field := range fieldsBefore[1:] {
			if strings.EqualFold(field, "as") {
				return nil
			}
		}
		return c.completeLoadPath(currentToken)
	}
}

func variableCompletionContext(currentToken string, fieldsBefore []string) (variableCompletionMode, bool) {
	if !strings.HasPrefix(currentToken, "@") || len(fieldsBefore) == 0 {
		return 0, false
	}
	first := strings.ToLower(fieldsBefore[0])
	last := strings.ToLower(fieldsBefore[len(fieldsBefore)-1])
	switch first {
	case "load", "resolve", "create":
		return variableCompletionAfterKeyword(last, "as", variableCompletionRuntime)
	case "read":
		if len(fieldsBefore) == 1 {
			return variableCompletionObjectOnly, true
		}
		return variableCompletionAfterKeyword(last, "as", variableCompletionRuntime)
	case "print":
		if len(fieldsBefore) == 1 {
			return variableCompletionRuntime, true
		}
	case "concat":
		if last == "as" {
			return variableCompletionRuntime, true
		}
		return variableCompletionTextOnly, true
	case "write":
		switch {
		case len(fieldsBefore) == 1:
			return variableCompletionTextOrList, true
		case last == "into":
			return variableCompletionObjectOnly, true
		case last == "as":
			return variableCompletionRuntime, true
		}
	case "copy", "move", "rename", "patch":
		if len(fieldsBefore) == 1 {
			return variableCompletionObjectOnly, true
		}
		return variableCompletionAfterKeyword(last, "as", variableCompletionRuntime)
	case "delete":
		if len(fieldsBefore) >= 2 {
			switch strings.ToLower(fieldsBefore[1]) {
			case "all", "file", "folder":
				return variableCompletionObjectOnly, true
			}
		}
	case "invoke":
		return invokeVariableCompletionContext(fieldsBefore, last)
	}
	return 0, false
}

func invokeVariableCompletionContext(fieldsBefore []string, last string) (variableCompletionMode, bool) {
	switch last {
	case "in":
		return variableCompletionConversation, true
	case "with":
		if containsFold(fieldsBefore, "concurrently") {
			return variableCompletionTextOrList, true
		}
		return variableCompletionTextOnly, true
	case "as":
		return variableCompletionRuntime, true
	}
	for _, field := range fieldsBefore[1:] {
		switch strings.ToLower(field) {
		case "in", "with", "as":
			return 0, false
		}
	}
	return variableCompletionTextOrList, true
}

func variableCompletionAfterKeyword(last string, keyword string, mode variableCompletionMode) (variableCompletionMode, bool) {
	if last == keyword {
		return mode, true
	}
	return 0, false
}

func (c *ReadlineCompleter) completeVariable(currentToken string, mode variableCompletionMode) []string {
	if c == nil || c.adapter == nil {
		return nil
	}
	variables := c.adapter.ListVariables()
	candidates := make([]string, 0, len(variables))
	for _, variable := range variables {
		if !matchesVariableMode(variable.Kind, mode) {
			continue
		}
		candidates = append(candidates, variable.Name)
	}
	return replaceCandidates(currentToken, filterByPrefix(candidates, currentToken))
}

func matchesVariableMode(kind string, mode variableCompletionMode) bool {
	switch mode {
	case variableCompletionRuntime:
		return kind != "conversation"
	case variableCompletionTextOnly:
		return kind == "text"
	case variableCompletionTextOrList:
		return kind == "text" || kind == "text_list"
	case variableCompletionObjectOnly:
		return kind == "object" || kind == "object_list"
	case variableCompletionConversation:
		return kind == "conversation"
	default:
		return false
	}
}

func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

func isVirtualPathContext(fieldsBefore []string) bool {
	if len(fieldsBefore) == 0 {
		return false
	}
	first := strings.ToLower(fieldsBefore[0])
	last := strings.ToLower(fieldsBefore[len(fieldsBefore)-1])
	switch first {
	case "cd", "ls":
		return len(fieldsBefore) == 1
	case "read":
		return len(fieldsBefore) == 1
	case "resolve", "delete":
		if len(fieldsBefore) < 2 {
			return false
		}
		kind := strings.ToLower(fieldsBefore[1])
		if kind != "file" && kind != "folder" && kind != "all" {
			return false
		}
		return true
	case "write", "copy", "move", "rename":
		return last == "to"
	default:
		return false
	}
}

func (c *ReadlineCompleter) completeVirtualPath(currentToken string) []string {
	dirPath, fragment, displayPrefix := splitVirtualCompletion(c.currentCWD(), currentToken)
	entries, err := c.adapter.ListVirtualDir(context.Background(), dirPath)
	if err != nil {
		return nil
	}
	return buildVirtualCandidates(entries, fragment, displayPrefix, currentToken)
}

func (c *ReadlineCompleter) completeLoadPath(currentToken string) []string {
	dirPath, fragment, displayPrefix := splitVirtualCompletion(c.currentLoadCWD(), currentToken)
	entries, err := c.adapter.ListLoadDir(context.Background(), dirPath)
	if err != nil {
		return nil
	}
	return buildLoadCandidates(entries, fragment, displayPrefix, currentToken)
}

func (c *ReadlineCompleter) currentCWD() string {
	if c == nil || c.adapter == nil {
		return "/"
	}
	return normalizeVirtualPath(c.adapter.CurrentCWD())
}

func (c *ReadlineCompleter) currentLoadCWD() string {
	if c == nil || c.adapter == nil {
		return "/"
	}
	return normalizeVirtualPath(c.adapter.CurrentLoadCWD())
}

func (c *ReadlineCompleter) currentSoftwareCWD() string {
	if c == nil || c.adapter == nil {
		return "/"
	}
	return normalizeVirtualPath(c.adapter.CurrentSoftwareCWD())
}

func (c *ReadlineCompleter) completeSoftwareRegistryPath(currentToken string) []string {
	reg := satis.DefaultSoftwareRegistry()
	if reg == nil {
		return nil
	}
	baseDir, fragment, displayPrefix := splitSoftwareRegistryCompletion(c.currentSoftwareCWD(), currentToken)
	folder, ok := reg.Folder(baseDir)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(folder.Entries)+2)
	if baseDir != "/" && strings.HasPrefix(strings.ToLower(".."), strings.ToLower(fragment)) {
		names = append(names, displayPrefix+"..")
	}
	if strings.HasPrefix(strings.ToLower("."), strings.ToLower(fragment)) {
		names = append(names, displayPrefix+".")
	}
	for _, entry := range folder.Entries {
		if entry.Kind != "folder" {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(entry.Name), strings.ToLower(fragment)) {
			continue
		}
		names = append(names, displayPrefix+entry.Name)
	}
	sort.Strings(names)
	return readlineSuffixes(currentToken, names)
}

func splitSoftwareRegistryCompletion(cwd string, token string) (string, string, string) {
	token = strings.TrimSpace(token)
	switch {
	case token == "", token == ".", token == "./":
		return normalizeVirtualPath(cwd), "", token
	case token == "..", token == "../":
		return normalizeVirtualPath(cwd), "..", ""
	case strings.HasSuffix(token, "/"):
		target := token
		if strings.HasPrefix(token, "/") {
			return normalizeVirtualPath(target), "", token
		}
		return normalizeVirtualPath(path.Join(cwd, token)), "", token
	case strings.HasPrefix(token, "/"):
		parent, frag := splitVirtualParent(token)
		return parent, frag, token[:len(token)-len(frag)]
	default:
		parent, frag := splitVirtualParent(token)
		if parent == "/" {
			return normalizeVirtualPath(cwd), frag, ""
		}
		fullParent := normalizeVirtualPath(path.Join(cwd, parent))
		return fullParent, frag, strings.TrimPrefix(parent, "/") + "/"
	}
}

func splitVirtualCompletion(cwd string, token string) (string, string, string) {
	token = strings.TrimSpace(token)
	switch {
	case token == "", token == ".", token == "./":
		return normalizeVirtualPath(cwd), "", token
	case strings.HasSuffix(token, "/"):
		if strings.HasPrefix(token, "/") {
			return normalizeVirtualPath(token), "", token
		}
		return normalizeVirtualPath(path.Join(cwd, token)), "", token
	case strings.HasPrefix(token, "/"):
		parent, frag := splitVirtualParent(token)
		return parent, frag, token[:len(token)-len(frag)]
	default:
		base := normalizeVirtualPath(cwd)
		if strings.HasPrefix(token, "./") {
			parent, frag := splitVirtualParent(token[2:])
			if parent == "/" {
				return base, frag, "./"
			}
			fullParent := normalizeVirtualPath(path.Join(base, strings.TrimPrefix(parent, "/")))
			return fullParent, frag, "./" + strings.TrimPrefix(parent, "/") + "/"
		}
		if strings.HasPrefix(token, "../") || token == ".." {
			parent, frag := splitVirtualParent(token)
			fullParent := normalizeVirtualPath(path.Join(base, parent))
			prefix := parent
			if prefix != "" && !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			return fullParent, frag, prefix
		}
		parent, frag := splitVirtualParent(token)
		if parent == "/" {
			return base, frag, ""
		}
		fullParent := normalizeVirtualPath(path.Join(base, parent))
		return fullParent, frag, strings.TrimPrefix(parent, "/") + "/"
	}
}

func splitVirtualParent(token string) (string, string) {
	token = strings.TrimSpace(token)
	token = strings.TrimSuffix(token, "/")
	if token == "" {
		return "/", ""
	}
	idx := strings.LastIndex(token, "/")
	if idx < 0 {
		return "/", token
	}
	parent := token[:idx]
	frag := token[idx+1:]
	if parent == "" {
		parent = "/"
	}
	return parent, frag
}

func buildVirtualCandidates(entries []vfs.DirEntry, fragment string, displayPrefix string, currentToken string) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.Name), strings.ToLower(fragment)) {
			continue
		}
		name := displayPrefix + entry.Name
		if entry.Kind == vfs.FileKindDirectory {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return readlineSuffixes(currentToken, names)
}

func buildLoadCandidates(entries []satis.LoadEntry, fragment string, displayPrefix string, currentToken string) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(strings.ToLower(entry.Name), strings.ToLower(fragment)) {
			continue
		}
		name := displayPrefix + entry.Name
		if entry.IsDir {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return readlineSuffixes(currentToken, names)
}

func (c *ReadlineCompleter) completeHostPath(currentToken string) []string {
	baseDir := "."
	fragment := currentToken
	displayPrefix := ""
	if currentToken != "" {
		if strings.HasSuffix(currentToken, "/") {
			baseDir = currentToken
			fragment = ""
			displayPrefix = filepath.ToSlash(strings.TrimSuffix(currentToken, "/")) + "/"
		}
		dirPart := filepath.Dir(currentToken)
		if fragment != "" && dirPart != "." {
			baseDir = dirPart
			fragment = filepath.Base(currentToken)
			displayPrefix = filepath.ToSlash(dirPart) + "/"
		}
	}

	dirPath, err := c.adapter.resolveHostPath(baseDir)
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}
	candidates := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(strings.ToLower(name), strings.ToLower(fragment)) {
			continue
		}
		candidate := displayPrefix + name
		if entry.IsDir() {
			candidate += "/"
		}
		candidates = append(candidates, candidate)
	}
	sort.Strings(candidates)
	return readlineSuffixes(currentToken, candidates)
}

// readlineSuffixes turns full completion strings into text to insert at the cursor.
// chzyer/readline inserts candidates at the cursor without removing the partial word.
func readlineSuffixes(typed string, fullMatches []string) []string {
	if len(fullMatches) == 0 {
		return nil
	}
	typedRunes := []rune(typed)
	out := make([]string, 0, len(fullMatches))
	for _, full := range fullMatches {
		fullRunes := []rune(full)
		if len(fullRunes) < len(typedRunes) {
			continue
		}
		if !strings.EqualFold(string(fullRunes[:len(typedRunes)]), typed) {
			continue
		}
		suffix := string(fullRunes[len(typedRunes):])
		if suffix == "" {
			continue
		}
		out = append(out, suffix)
	}
	return out
}

func replaceCandidates(currentToken string, replacements []string) []string {
	return readlineSuffixes(currentToken, replacements)
}

func mergeCandidates(groups ...[]string) []string {
	if len(groups) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, group := range groups {
		for _, candidate := range group {
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			merged = append(merged, candidate)
		}
	}
	sort.Strings(merged)
	return merged
}

func filterByPrefix(items []string, prefix string) []string {
	matches := make([]string, 0, len(items))
	lowerPrefix := strings.ToLower(prefix)
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), lowerPrefix) {
			matches = append(matches, item)
		}
	}
	sort.Strings(matches)
	return matches
}

func tokenStartIndex(line []rune, pos int) int {
	start := pos
	for start > 0 && !unicode.IsSpace(line[start-1]) {
		start--
	}
	return start
}

func normalizeVirtualPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	cleaned := path.Clean(raw)
	if cleaned == "." {
		return "/"
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	return cleaned
}
