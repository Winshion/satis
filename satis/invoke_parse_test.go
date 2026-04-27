package satis

import "testing"

func TestParseBodyInvokeViaProvider(t *testing.T) {
	instructions, err := ParseBody("Invoke [[[hello]]] with @input via fast as @out")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	stmt, ok := instructions[0].(InvokeStmt)
	if !ok {
		t.Fatalf("expected InvokeStmt, got %T", instructions[0])
	}
	if stmt.Provider != "fast" {
		t.Fatalf("expected provider fast, got %q", stmt.Provider)
	}
	if stmt.OutputVar != "@out" {
		t.Fatalf("expected output var @out, got %q", stmt.OutputVar)
	}
}

func TestParseBodyInvokeViaProviderWithoutInput(t *testing.T) {
	instructions, err := ParseBody("Invoke [[[hello]]] via minimax-m2.5 as @f")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	stmt, ok := instructions[0].(InvokeStmt)
	if !ok {
		t.Fatalf("expected InvokeStmt, got %T", instructions[0])
	}
	if stmt.Provider != "minimax-m2.5" {
		t.Fatalf("expected provider minimax-m2.5, got %q", stmt.Provider)
	}
	if stmt.OutputVar != "@f" {
		t.Fatalf("expected output var @f, got %q", stmt.OutputVar)
	}
}

func TestParseBodyBatchInvokeViaProvider(t *testing.T) {
	instructions, err := ParseBody("Invoke [[[hello]]] concurrently with @items via fast as @out mode single_file")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	stmt, ok := instructions[0].(BatchInvokeStmt)
	if !ok {
		t.Fatalf("expected BatchInvokeStmt, got %T", instructions[0])
	}
	if stmt.Provider != "fast" {
		t.Fatalf("expected provider fast, got %q", stmt.Provider)
	}
	if stmt.OutputMode != "single_file" {
		t.Fatalf("expected output mode single_file, got %q", stmt.OutputMode)
	}
}

func TestParseBodyInvokeProviderUpsert(t *testing.T) {
	instructions, err := ParseBody("Invoke provider upsert fast --base-url https://example.test/v1 --model demo as @result")
	if err != nil {
		t.Fatalf("ParseBody returned error: %v", err)
	}
	stmt, ok := instructions[0].(InvokeProviderStmt)
	if !ok {
		t.Fatalf("expected InvokeProviderStmt, got %T", instructions[0])
	}
	if stmt.Action != "upsert" || stmt.Name != "fast" {
		t.Fatalf("unexpected provider stmt: %#v", stmt)
	}
	if stmt.OutputVar != "@result" {
		t.Fatalf("expected output var @result, got %q", stmt.OutputVar)
	}
	if len(stmt.Flags) != 2 {
		t.Fatalf("expected 2 flags, got %d", len(stmt.Flags))
	}
}
