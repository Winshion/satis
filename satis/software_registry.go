package satis

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type SoftwareRegistry struct {
	Root    string
	ByName  map[string]SoftwareSpec
	Folders map[string]SoftwareFolder
}

type SoftwareSpec struct {
	ID           string                      `json:"-"`
	Name         string                      `json:"name"`
	Locator      SoftwareLocator             `json:"locator"`
	Runner       SoftwareRunner              `json:"runner"`
	Functions    map[string]SoftwareFunction `json:"functions"`
	Dir          string                      `json:"-"`
	SkillPath    string                      `json:"-"`
	RegistryPath string                      `json:"-"`
	Description  string                      `json:"-"`
}

type SoftwareFolder struct {
	Path        string
	Dir         string
	Description string
	Entries     []SoftwareFolderEntry
}

type SoftwareFolderEntry struct {
	Name        string
	Description string
	Path        string
	Kind        string // software|folder|file
}

type SoftwareLocator struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
}

type SoftwareRunner struct {
	Kind    string `json:"kind"`
	Command string `json:"command,omitempty"`
	Script  string `json:"script,omitempty"`
	Module  string `json:"module,omitempty"`
}

type SoftwareFunction struct {
	Args               []string `json:"args"`
	Returns            string   `json:"returns"`
	EstimatedRuntimeMS int      `json:"estimated_runtime_ms,omitempty"`
}

type SoftwareRefreshReport struct {
	RefreshedFolders   int
	RecognizedSoftware int
	SkippedSoftware    int
}

type skillDocMeta struct {
	Name        string
	Description string
}

type folderSkillDoc struct {
	Meta              skillDocMeta
	EntryDescriptions map[string]string
}

var (
	defaultSoftwareRegistry struct {
		mu  sync.RWMutex
		reg *SoftwareRegistry
	}
	errInvalidSkillDoc = errors.New("invalid skill doc")
)

func SetDefaultSoftwareRegistry(reg *SoftwareRegistry) {
	defaultSoftwareRegistry.mu.Lock()
	defer defaultSoftwareRegistry.mu.Unlock()
	defaultSoftwareRegistry.reg = reg
}

func DefaultSoftwareRegistry() *SoftwareRegistry {
	defaultSoftwareRegistry.mu.RLock()
	defer defaultSoftwareRegistry.mu.RUnlock()
	return defaultSoftwareRegistry.reg
}

func (r *SoftwareRegistry) Lookup(name string) (SoftwareSpec, bool) {
	if r == nil {
		return SoftwareSpec{}, false
	}
	spec, ok := r.ByName[name]
	return spec, ok
}

func (r *SoftwareRegistry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.ByName))
	for name := range r.ByName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *SoftwareRegistry) Folder(pathValue string) (SoftwareFolder, bool) {
	if r == nil {
		return SoftwareFolder{}, false
	}
	folder, ok := r.Folders[normalizeRegistryPath(pathValue)]
	return folder, ok
}

func (r *SoftwareRegistry) FindByPrefix(prefix string) []SoftwareSpec {
	if r == nil {
		return nil
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	out := make([]SoftwareSpec, 0)
	for _, spec := range r.ByName {
		if strings.HasPrefix(strings.ToLower(spec.Name), prefix) {
			out = append(out, spec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].RegistryPath < out[j].RegistryPath
	})
	return out
}

func (r *SoftwareRegistry) ResolveFolderPath(cwd string, raw string) (string, error) {
	if r == nil {
		return "/", fmt.Errorf("software registry is not configured")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return normalizeRegistryPath(cwd), nil
	}
	var target string
	if strings.HasPrefix(raw, "/") {
		target = normalizeRegistryPath(raw)
	} else {
		target = normalizeRegistryPath(path.Join(normalizeRegistryPath(cwd), raw))
	}
	if _, ok := r.Folder(target); !ok {
		return "", fmt.Errorf("software folder %q not found", target)
	}
	return target, nil
}

func (r *SoftwareRegistry) Refresh() (*SoftwareRegistry, SoftwareRefreshReport, error) {
	if r == nil || strings.TrimSpace(r.Root) == "" {
		return nil, SoftwareRefreshReport{}, fmt.Errorf("software registry is not configured")
	}
	report := SoftwareRefreshReport{}
	if err := refreshRegistryFolder(r.Root, "/", &report); err != nil {
		return nil, report, err
	}
	reloaded, err := LoadSoftwareRegistry(r.Root)
	if err != nil {
		return nil, report, err
	}
	return reloaded, report, nil
}

func LoadSoftwareRegistry(root string) (*SoftwareRegistry, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("software registry error: %q is not a directory", root)
	}
	reg := &SoftwareRegistry{
		Root:    root,
		ByName:  make(map[string]SoftwareSpec),
		Folders: make(map[string]SoftwareFolder),
	}
	if err := reg.loadFolder(root, "/"); err != nil {
		return nil, err
	}
	return reg, nil
}

func (r *SoftwareRegistry) loadFolder(dir string, registryPath string) error {
	doc, err := readFolderSkillDoc(filepath.Join(dir, "SKILLS.md"))
	if err != nil {
		return err
	}
	folder := SoftwareFolder{
		Path:        registryPath,
		Dir:         dir,
		Description: doc.Meta.Description,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "SKILLS.md" || strings.HasPrefix(name, ".") {
			continue
		}
		abs := filepath.Join(dir, name)
		desc := doc.EntryDescriptions[name]
		if entry.IsDir() {
			if fileExists(filepath.Join(abs, "forsatis.json")) {
				spec, err := loadSoftwareSpec(abs)
				if err != nil {
					continue
				}
				if _, exists := r.ByName[spec.Name]; exists {
					return fmt.Errorf("software registry error: duplicate software name %q", spec.Name)
				}
				spec.ID = "software:" + spec.Name
				spec.RegistryPath = normalizeRegistryPath(path.Join(registryPath, name))
				if spec.Description == "" {
					spec.Description = desc
				}
				r.ByName[spec.Name] = spec
				folder.Entries = append(folder.Entries, SoftwareFolderEntry{
					Name:        spec.Name,
					Description: spec.Description,
					Path:        spec.RegistryPath,
					Kind:        "software",
				})
				continue
			}
			childPath := normalizeRegistryPath(path.Join(registryPath, name))
			if err := r.loadFolder(abs, childPath); err != nil {
				return err
			}
			childDesc := desc
			if childDesc == "" {
				if child, ok := r.Folder(childPath); ok {
					childDesc = child.Description
				}
			}
			folder.Entries = append(folder.Entries, SoftwareFolderEntry{
				Name:        name,
				Description: childDesc,
				Path:        childPath,
				Kind:        "folder",
			})
			continue
		}
		folder.Entries = append(folder.Entries, SoftwareFolderEntry{
			Name:        name,
			Description: desc,
			Path:        normalizeRegistryPath(path.Join(registryPath, name)),
			Kind:        "file",
		})
	}
	sort.Slice(folder.Entries, func(i, j int) bool {
		if folder.Entries[i].Kind != folder.Entries[j].Kind {
			return folder.Entries[i].Kind < folder.Entries[j].Kind
		}
		return folder.Entries[i].Name < folder.Entries[j].Name
	})
	r.Folders[registryPath] = folder
	return nil
}

func loadSoftwareSpec(dir string) (SoftwareSpec, error) {
	data, err := os.ReadFile(filepath.Join(dir, "forsatis.json"))
	if err != nil {
		return SoftwareSpec{}, err
	}
	var spec SoftwareSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return SoftwareSpec{}, err
	}
	spec.Dir = dir
	spec.SkillPath = filepath.Join(dir, "SKILL.md")
	if strings.TrimSpace(spec.Name) == "" {
		return SoftwareSpec{}, fmt.Errorf("software registry error: missing name in %s", dir)
	}
	if len(spec.Functions) == 0 {
		return SoftwareSpec{}, fmt.Errorf("software registry error: software %q must declare at least one function", spec.Name)
	}
	for fn, def := range spec.Functions {
		if strings.TrimSpace(fn) == "" {
			return SoftwareSpec{}, fmt.Errorf("software registry error: software %q has empty function name", spec.Name)
		}
		if def.Returns != "string" {
			return SoftwareSpec{}, fmt.Errorf("software registry error: software %q function %q returns must be string", spec.Name, fn)
		}
		seen := map[string]struct{}{}
		for _, arg := range def.Args {
			if !strings.HasPrefix(arg, "--") {
				return SoftwareSpec{}, fmt.Errorf("software registry error: software %q function %q arg %q must start with --", spec.Name, fn, arg)
			}
			if _, ok := seen[arg]; ok {
				return SoftwareSpec{}, fmt.Errorf("software registry error: software %q function %q duplicate arg %q", spec.Name, fn, arg)
			}
			seen[arg] = struct{}{}
		}
	}
	meta, err := parseSkillDocMetadata(spec.SkillPath)
	if err != nil {
		return SoftwareSpec{}, err
	}
	if meta.Name != spec.Name {
		return SoftwareSpec{}, fmt.Errorf("software registry error: skill doc name %q does not match software name %q", meta.Name, spec.Name)
	}
	spec.Description = meta.Description
	return spec, nil
}

func (s SoftwareSpec) SupportsFunction(name string) (SoftwareFunction, bool) {
	fn, ok := s.Functions[name]
	return fn, ok
}

func (f SoftwareFunction) AcceptsFlag(name string) bool {
	for _, arg := range f.Args {
		if arg == name {
			return true
		}
	}
	return false
}

func parseFolderSkills(path string) (map[string]string, error) {
	doc, err := readFolderSkillDoc(path)
	if err != nil {
		return nil, err
	}
	return doc.EntryDescriptions, nil
}

func readFolderSkillDoc(path string) (folderSkillDoc, error) {
	doc := folderSkillDoc{EntryDescriptions: map[string]string{}}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, fmt.Errorf("software registry error: open SKILLS.md at %s: %w", filepath.Dir(path), err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lineNo := 0
	inFrontMatter := false
	frontMatterParsed := false
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if lineNo == 1 && line == "---" {
			inFrontMatter = true
			frontMatterParsed = true
			continue
		}
		if inFrontMatter {
			if line == "---" {
				inFrontMatter = false
				continue
			}
			if !strings.Contains(line, ":") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			switch key {
			case "name":
				doc.Meta.Name = value
			case "description":
				doc.Meta.Description = value
				doc.EntryDescriptions["."] = value
			}
			continue
		}
		if !frontMatterParsed && strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "-") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.ReplaceAll(line, "`", "")
		var name, desc string
		switch {
		case strings.Contains(line, ":"):
			parts := strings.SplitN(line, ":", 2)
			name = strings.TrimSpace(parts[0])
			desc = strings.TrimSpace(parts[1])
		case strings.Contains(line, " - "):
			parts := strings.SplitN(line, " - ", 2)
			name = strings.TrimSpace(parts[0])
			desc = strings.TrimSpace(parts[1])
		default:
			continue
		}
		if name != "" {
			doc.EntryDescriptions[name] = desc
		}
	}
	if err := scanner.Err(); err != nil {
		return folderSkillDoc{}, err
	}
	return doc, nil
}

func parseSkillDocMetadata(path string) (skillDocMeta, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return skillDocMeta{}, fmt.Errorf("software registry error: missing SKILL.md at %s", filepath.Dir(path))
		}
		return skillDocMeta{}, fmt.Errorf("software registry error: open SKILL.md at %s: %w", filepath.Dir(path), err)
	}
	defer file.Close()
	meta, ok, err := parseSkillDocMetadataFromScanner(bufio.NewScanner(file))
	if err != nil {
		return skillDocMeta{}, err
	}
	if !ok {
		return skillDocMeta{}, fmt.Errorf("software registry error: invalid SKILL.md format at %s: %w", filepath.Dir(path), errInvalidSkillDoc)
	}
	if meta.Name == "" || meta.Description == "" {
		return skillDocMeta{}, fmt.Errorf("software registry error: SKILL.md at %s must declare non-empty name and description", filepath.Dir(path))
	}
	return meta, nil
}

func parseSkillDocMetadataFromScanner(scanner *bufio.Scanner) (skillDocMeta, bool, error) {
	lineNo := 0
	inFrontMatter := false
	meta := skillDocMeta{}
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if lineNo == 1 {
			if line != "---" {
				return skillDocMeta{}, false, nil
			}
			inFrontMatter = true
			continue
		}
		if inFrontMatter {
			if line == "---" {
				return meta, true, scanner.Err()
			}
			if !strings.Contains(line, ":") {
				continue
			}
			parts := strings.SplitN(line, ":", 2)
			key := strings.ToLower(strings.TrimSpace(parts[0]))
			value := strings.TrimSpace(parts[1])
			switch key {
			case "name":
				meta.Name = value
			case "description":
				meta.Description = value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return skillDocMeta{}, false, err
	}
	return skillDocMeta{}, false, nil
}

func refreshRegistryFolder(dir string, registryPath string, report *SoftwareRefreshReport) error {
	doc, err := readFolderSkillDoc(filepath.Join(dir, "SKILLS.md"))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	folderEntries := make([]SoftwareFolderEntry, 0)
	for _, entry := range entries {
		name := entry.Name()
		if name == "SKILLS.md" || strings.HasPrefix(name, ".") {
			continue
		}
		abs := filepath.Join(dir, name)
		if !entry.IsDir() {
			continue
		}
		if fileExists(filepath.Join(abs, "forsatis.json")) {
			spec, err := loadSoftwareSpec(abs)
			if err != nil {
				if report != nil {
					report.SkippedSoftware++
				}
				continue
			}
			if report != nil {
				report.RecognizedSoftware++
			}
			folderEntries = append(folderEntries, SoftwareFolderEntry{
				Name:        spec.Name,
				Description: spec.Description,
				Path:        normalizeRegistryPath(path.Join(registryPath, name)),
				Kind:        "software",
			})
			continue
		}
		childPath := normalizeRegistryPath(path.Join(registryPath, name))
		if err := refreshRegistryFolder(abs, childPath, report); err != nil {
			return err
		}
		childDoc, err := readFolderSkillDoc(filepath.Join(abs, "SKILLS.md"))
		if err != nil {
			return err
		}
		childDesc := strings.TrimSpace(childDoc.Meta.Description)
		if childDesc == "" {
			childDesc = defaultFolderDescription(childPath)
		}
		folderEntries = append(folderEntries, SoftwareFolderEntry{
			Name:        name,
			Description: childDesc,
			Path:        childPath,
			Kind:        "folder",
		})
	}
	sort.Slice(folderEntries, func(i, j int) bool {
		if folderEntries[i].Kind != folderEntries[j].Kind {
			return folderEntries[i].Kind < folderEntries[j].Kind
		}
		return folderEntries[i].Name < folderEntries[j].Name
	})
	meta := doc.Meta
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = defaultFolderName(registryPath)
	}
	if strings.TrimSpace(meta.Description) == "" {
		meta.Description = defaultFolderDescription(registryPath)
	}
	content := renderFolderSkillsDocument(meta, folderEntries)
	changed, err := writeTextIfChanged(filepath.Join(dir, "SKILLS.md"), content)
	if err != nil {
		return err
	}
	if changed && report != nil {
		report.RefreshedFolders++
	}
	return nil
}

func renderFolderSkillsDocument(meta skillDocMeta, entries []SoftwareFolderEntry) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(meta.Name)
	b.WriteString("\n")
	b.WriteString("description: ")
	b.WriteString(meta.Description)
	b.WriteString("\n")
	b.WriteString("---\n\n")
	b.WriteString("## Entries\n\n")
	for _, entry := range entries {
		b.WriteString("- ")
		b.WriteString(entry.Name)
		b.WriteString(": ")
		b.WriteString(entry.Description)
		b.WriteString("\n")
	}
	return b.String()
}

func writeTextIfChanged(path string, content string) (bool, error) {
	if existing, err := os.ReadFile(path); err == nil {
		if string(existing) == content {
			return false, nil
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func defaultFolderName(registryPath string) string {
	if registryPath == "/" {
		return "software_registry"
	}
	return path.Base(registryPath)
}

func defaultFolderDescription(registryPath string) string {
	if registryPath == "/" {
		return "Root index of registered software folders and software entries."
	}
	return fmt.Sprintf("Index of software entries under %s.", defaultFolderName(registryPath))
}

func normalizeRegistryPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return path.Clean(p)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
