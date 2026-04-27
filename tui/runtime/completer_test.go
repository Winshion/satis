package tuiruntime

import (
	"testing"

	"satis/satis"
)

func TestCompleteSoftwareManageSubcommands(t *testing.T) {
	completer := &ReadlineCompleter{}
	got := completer.completeInstruction("", []string{"Software"})
	want := []string{"cd", "describe", "find", "functions", "ls", "pwd", "readskill", "refresh"}
	assertSameStrings(t, got, want)
}

func TestCompleteSoftwareManageSoftwareNamesAndAs(t *testing.T) {
	prev := satis.DefaultSoftwareRegistry()
	t.Cleanup(func() { satis.SetDefaultSoftwareRegistry(prev) })
	satis.SetDefaultSoftwareRegistry(&satis.SoftwareRegistry{
		ByName: map[string]satis.SoftwareSpec{
			"alpha": {Name: "alpha", Functions: map[string]satis.SoftwareFunction{"run": {Returns: "string"}}},
			"beta":  {Name: "beta", Functions: map[string]satis.SoftwareFunction{"run": {Returns: "string"}}},
		},
	})

	completer := &ReadlineCompleter{}
	gotNames := completer.completeInstruction("", []string{"Software", "describe"})
	assertSameStrings(t, gotNames, []string{"alpha", "beta"})

	gotAs := completer.completeInstruction("", []string{"Software", "describe", "alpha"})
	assertSameStrings(t, gotAs, []string{"as"})
}

func TestCompleteSoftwareManageRegistryPaths(t *testing.T) {
	prev := satis.DefaultSoftwareRegistry()
	t.Cleanup(func() { satis.SetDefaultSoftwareRegistry(prev) })
	satis.SetDefaultSoftwareRegistry(&satis.SoftwareRegistry{
		ByName: map[string]satis.SoftwareSpec{},
		Folders: map[string]satis.SoftwareFolder{
			"/": {
				Path: "/",
				Entries: []satis.SoftwareFolderEntry{
					{Name: "chem", Kind: "folder", Path: "/chem"},
					{Name: "tools", Kind: "folder", Path: "/tools"},
					{Name: "note.md", Kind: "file", Path: "/note.md"},
				},
			},
		},
	})

	completer := &ReadlineCompleter{}
	got := completer.completeInstruction("", []string{"Software", "cd"})
	assertSameStrings(t, got, []string{".", "chem", "tools"})
}

func TestCompleteSoftwareInstructionFlagsAndAs(t *testing.T) {
	prev := satis.DefaultSoftwareRegistry()
	t.Cleanup(func() { satis.SetDefaultSoftwareRegistry(prev) })
	satis.SetDefaultSoftwareRegistry(&satis.SoftwareRegistry{
		ByName: map[string]satis.SoftwareSpec{
			"alpha": {
				Name: "alpha",
				Functions: map[string]satis.SoftwareFunction{
					"run": {Args: []string{"--input", "--mode"}, Returns: "string"},
				},
			},
		},
	})

	completer := &ReadlineCompleter{}

	gotAfterFunction := completer.completeInstruction("", []string{"alpha", "run"})
	assertSameStrings(t, gotAfterFunction, []string{"--input", "--mode", "as"})

	gotAfterValue := completer.completeInstruction("", []string{"alpha", "run", "--input", "demo"})
	assertSameStrings(t, gotAfterValue, []string{"--mode", "as"})

	gotAfterUsedAllFlags := completer.completeInstruction("", []string{"alpha", "run", "--input", "demo", "--mode", "fast"})
	assertSameStrings(t, gotAfterUsedAllFlags, []string{"as"})
}

func TestAnalyzeSoftwareCallCompletion(t *testing.T) {
	state := analyzeSoftwareCallCompletion([]string{"--input", "demo", "--mode"})
	if !state.expectFlagValue {
		t.Fatalf("expected missing flag value to be detected")
	}
	if _, ok := state.usedFlags["--input"]; !ok {
		t.Fatalf("expected --input to be tracked as used")
	}
	if _, ok := state.usedFlags["--mode"]; !ok {
		t.Fatalf("expected --mode to be tracked as used")
	}

	state = analyzeSoftwareCallCompletion([]string{"--input", "demo", "as"})
	if !state.expectRuntimeVar {
		t.Fatalf("expected as-clause to require runtime variable")
	}
}

func TestSoftwareFlagCandidatesSkipsUsedFlags(t *testing.T) {
	got := softwareFlagCandidates([]string{"--input", "--mode"}, map[string]struct{}{"--input": {}})
	assertSameStrings(t, got, []string{"--mode"})
}

func TestCompleteSoftwareInstructionFlagPrefix(t *testing.T) {
	prev := satis.DefaultSoftwareRegistry()
	t.Cleanup(func() { satis.SetDefaultSoftwareRegistry(prev) })
	satis.SetDefaultSoftwareRegistry(&satis.SoftwareRegistry{
		ByName: map[string]satis.SoftwareSpec{
			"alpha": {
				Name: "alpha",
				Functions: map[string]satis.SoftwareFunction{
					"run": {Args: []string{"--input", "--mode"}, Returns: "string"},
				},
			},
		},
	})

	completer := &ReadlineCompleter{}
	got := completer.completeInstruction("--", []string{"alpha", "run"})
	assertSameStrings(t, got, []string{"input", "mode"})
}

func assertSameStrings(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at %d: got %v want %v", i, got, want)
		}
	}
}
