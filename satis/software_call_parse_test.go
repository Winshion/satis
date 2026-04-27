package satis

import "testing"

func TestParseBodySoftwareCallAcceptsBareStringFlags(t *testing.T) {
	prev := DefaultSoftwareRegistry()
	SetDefaultSoftwareRegistry(&SoftwareRegistry{
		ByName: map[string]SoftwareSpec{
			"zcnn": {
				Name: "zcnn",
				Functions: map[string]SoftwareFunction{
					"train": {
						Args:    []string{"--samples", "--epochs", "--seed"},
						Returns: "string",
					},
				},
			},
		},
	})
	t.Cleanup(func() {
		SetDefaultSoftwareRegistry(prev)
	})

	instructions, err := ParseBody("zcnn train --samples 64 --epochs 10 --seed 12345 as @return")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	if len(instructions) != 1 {
		t.Fatalf("expected 1 instruction, got %d", len(instructions))
	}

	stmt, ok := instructions[0].(SoftwareCallStmt)
	if !ok {
		t.Fatalf("expected SoftwareCallStmt, got %T", instructions[0])
	}
	if stmt.OutputVar != "@return" {
		t.Fatalf("expected output var @return, got %q", stmt.OutputVar)
	}
	if len(stmt.Flags) != 3 {
		t.Fatalf("expected 3 flags, got %d", len(stmt.Flags))
	}
	if stmt.Flags[0].Value.Kind != ValueKindString || stmt.Flags[0].Value.Text != "64" {
		t.Fatalf("expected --samples to parse as string 64, got kind=%q text=%q", stmt.Flags[0].Value.Kind, stmt.Flags[0].Value.Text)
	}
	if stmt.Flags[1].Value.Kind != ValueKindString || stmt.Flags[1].Value.Text != "10" {
		t.Fatalf("expected --epochs to parse as string 10, got kind=%q text=%q", stmt.Flags[1].Value.Kind, stmt.Flags[1].Value.Text)
	}
	if stmt.Flags[2].Value.Kind != ValueKindString || stmt.Flags[2].Value.Text != "12345" {
		t.Fatalf("expected --seed to parse as string 12345, got kind=%q text=%q", stmt.Flags[2].Value.Kind, stmt.Flags[2].Value.Text)
	}
}

func TestParseBodySoftwareCallStripsTripleBracketFlags(t *testing.T) {
	prev := DefaultSoftwareRegistry()
	SetDefaultSoftwareRegistry(&SoftwareRegistry{
		ByName: map[string]SoftwareSpec{
			"zcnn": {
				Name: "zcnn",
				Functions: map[string]SoftwareFunction{
					"train": {
						Args:    []string{"--samples"},
						Returns: "string",
					},
				},
			},
		},
	})
	t.Cleanup(func() {
		SetDefaultSoftwareRegistry(prev)
	})

	instructions, err := ParseBody("zcnn train --samples [[[64]]]")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	stmt, ok := instructions[0].(SoftwareCallStmt)
	if !ok {
		t.Fatalf("expected SoftwareCallStmt, got %T", instructions[0])
	}
	if len(stmt.Flags) != 1 {
		t.Fatalf("expected 1 flag, got %d", len(stmt.Flags))
	}
	if stmt.Flags[0].Value.Kind != ValueKindString || stmt.Flags[0].Value.Text != "64" {
		t.Fatalf("expected triple-bracket value to become plain string 64, got kind=%q text=%q", stmt.Flags[0].Value.Kind, stmt.Flags[0].Value.Text)
	}
}
