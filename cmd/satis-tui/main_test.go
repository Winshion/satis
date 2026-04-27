package main

import "testing"

func TestExplicitBatchCollectorKeepsDefaultPromptWhenIdle(t *testing.T) {
	c := explicitBatchCollector{}

	if got := c.prompt("satis (line)> "); got != "satis (line)> " {
		t.Fatalf("unexpected prompt: %q", got)
	}
}

func TestExplicitBatchCollectorSuppressesPromptUntilBatchEnds(t *testing.T) {
	c := explicitBatchCollector{}

	ready, batch := c.push(">>> cmd1")
	if ready || batch != "" {
		t.Fatalf("expected collector to keep buffering, got ready=%v batch=%q", ready, batch)
	}
	if got := c.prompt("satis (line)> "); got != "" {
		t.Fatalf("expected empty continuation prompt, got %q", got)
	}

	ready, batch = c.push("cmd2<<<")
	if !ready || batch != ">>> cmd1\ncmd2<<<" {
		t.Fatalf("unexpected collected batch: ready=%v batch=%q", ready, batch)
	}
	if got := c.prompt("satis (line)> "); got != "satis (line)> " {
		t.Fatalf("expected prompt to restore after batch, got %q", got)
	}
}

func TestExplicitBatchCollectorPassesThroughSingleLineBatch(t *testing.T) {
	c := explicitBatchCollector{}

	ready, batch := c.push(">>> cmd1 <<<")
	if !ready || batch != ">>> cmd1 <<<" {
		t.Fatalf("unexpected pass-through batch: ready=%v batch=%q", ready, batch)
	}
}

func TestExplicitBatchCollectorCancelClearsMultilineState(t *testing.T) {
	c := explicitBatchCollector{}
	ready, _ := c.push(">>> cmd1")
	if ready {
		t.Fatalf("expected collector to stay buffering after multiline start")
	}

	if !c.cancel() {
		t.Fatalf("expected cancel to report active collector")
	}
	if c.active() {
		t.Fatalf("expected collector to be inactive after cancel")
	}
	if got := c.prompt("satis (line)> "); got != "satis (line)> " {
		t.Fatalf("expected default prompt after cancel, got %q", got)
	}
}

func TestExplicitBatchCollectorCancelIdleNoop(t *testing.T) {
	c := explicitBatchCollector{}
	if c.cancel() {
		t.Fatalf("expected cancel to return false when idle")
	}
}
