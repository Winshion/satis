package satis

import (
	"os"
	"strings"
	"testing"
)

func TestParseBodySoftwareReadskill(t *testing.T) {
	instructions, err := ParseBody("Software readskill demo as @skilldoc")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}
	stmt, ok := instructions[0].(SoftwareManageStmt)
	if !ok {
		t.Fatalf("expected SoftwareManageStmt, got %T", instructions[0])
	}
	if stmt.Action != "readskill" {
		t.Fatalf("expected action readskill, got %q", stmt.Action)
	}
	if stmt.Arg != "demo" {
		t.Fatalf("expected arg demo, got %q", stmt.Arg)
	}
	if stmt.OutputVar != "@skilldoc" {
		t.Fatalf("expected output var @skilldoc, got %q", stmt.OutputVar)
	}
}

func TestExecuteSoftwareManageReadskillReturnsFullSkillDoc(t *testing.T) {
	const skillText = `---
name: demo
description: Demo description.
---

# Demo

Usage details here.

- Step 1
- Step 2
`
	reg := &SoftwareRegistry{
		ByName: map[string]SoftwareSpec{
			"demo": {
				Name:      "demo",
				SkillPath: writeTempFile(t, "SKILL.md", skillText),
				Functions: map[string]SoftwareFunction{
					"run": {Returns: "string"},
				},
			},
		},
		Folders: map[string]SoftwareFolder{"/": {Path: "/"}},
	}
	exec := &Executor{SoftwareRegistry: reg}

	got, err := exec.executeSoftwareManage(SoftwareManageStmt{Action: "readskill", Arg: "demo"}, nil)
	if err != nil {
		t.Fatalf("executeSoftwareManage returned error: %v", err)
	}
	if got != skillText {
		t.Fatalf("unexpected skill text:\n--- got ---\n%s\n--- want ---\n%s", got, skillText)
	}
	if !strings.Contains(got, "Usage details here.") {
		t.Fatalf("expected full skill document content, got %q", got)
	}
}

func writeTempFile(t *testing.T, name string, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}
