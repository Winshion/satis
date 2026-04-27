package satis

import "fmt"

// Validate applies the current v1 semantic checks to a parsed chunk.
//
// Parsing answers "can this be read as syntax".
// Validation answers "is this chunk legal under the current v1 contract".
func Validate(chunk *Chunk) error {
	if chunk == nil {
		return fmt.Errorf("satis validate error: chunk is nil")
	}

	if chunk.Meta["chunk_id"] == "" {
		return fmt.Errorf("satis validate error: missing required header chunk_id")
	}
	if chunk.Meta["intent_uid"] == "" {
		return fmt.Errorf("satis validate error: missing required header intent_uid")
	}

	objectKinds := make(map[string]ResolveTargetKind)

	for idx, inst := range chunk.Instructions {
		switch stmt := inst.(type) {
		case CdStmt, PwdStmt, LsStmt, LoadPwdStmt, LoadCdStmt, LoadLsStmt, CommitStmt, RollbackStmt:
			continue

		case SoftwareManageStmt, InvokeProviderStmt:
			continue

		case ResolveStmt:
			objectKinds[stmt.OutputVar] = stmt.TargetKind

		case CreateStmt:
			if len(stmt.Paths) == 0 {
				return validateError(idx, "create requires at least one path")
			}
			if stmt.TargetKind == CreateTargetFolder && stmt.HasContent {
				return validateError(idx, "create folder does not support initial content")
			}
			if stmt.OutputVar != "" && len(stmt.Paths) == 1 {
				if stmt.TargetKind == CreateTargetFolder {
					objectKinds[stmt.OutputVar] = ResolveTargetFolder
				} else {
					objectKinds[stmt.OutputVar] = ResolveTargetFile
				}
			}

		case LoadStmt:
			if len(stmt.Sources) == 0 {
				return validateError(idx, "load requires at least one source path")
			}
			if stmt.OutputVar != "" {
				objectKinds[stmt.OutputVar] = ResolveTargetFile
			}

		case ReadStmt:
			if stmt.OutputVar == "" {
				return validateError(idx, "read requires output variable")
			}
			if (stmt.ObjectVar == "" && stmt.Path == "") || (stmt.ObjectVar != "" && stmt.Path != "") {
				return validateError(idx, "read requires exactly one source")
			}
			if stmt.ObjectVar != "" {
				if kind, ok := objectKinds[stmt.ObjectVar]; ok && kind == ResolveTargetFolder && stmt.StartLine == 0 && stmt.EndLine == 0 {
					return validateError(idx, "read on folder is reserved in v1")
				}
			}

		case WriteStmt:
			if (stmt.Path == "" && stmt.ObjectVar == "") || (stmt.Path != "" && stmt.ObjectVar != "") {
				return validateError(idx, "write requires exactly one target")
			}
			if stmt.ObjectVar != "" {
				if kind, ok := objectKinds[stmt.ObjectVar]; ok && kind == ResolveTargetFolder {
					return validateError(idx, "write target object must be a file")
				}
			}
			if stmt.OutputVar != "" {
				objectKinds[stmt.OutputVar] = ResolveTargetFile
			}

		case PrintStmt:
			if stmt.Value.Kind != ValueKindString && stmt.Value.Kind != ValueKindVariable {
				return validateError(idx, "print requires a string literal or variable")
			}

		case ConcatStmt:
			if stmt.OutputVar == "" {
				return validateError(idx, "concat requires output variable")
			}
			if len(stmt.Values) == 0 {
				return validateError(idx, "concat requires at least one value")
			}
			for _, value := range stmt.Values {
				if value.Kind != ValueKindString && value.Kind != ValueKindVariable {
					return validateError(idx, "concat only supports string literals or variables")
				}
			}

		case CopyStmt:
			if stmt.ObjectVar == "" {
				return validateError(idx, "copy requires input variable")
			}
			if kind, ok := objectKinds[stmt.ObjectVar]; ok && kind == ResolveTargetFolder {
				return validateError(idx, "copy on folder is reserved in v1")
			}
			if stmt.OutputVar != "" {
				objectKinds[stmt.OutputVar] = ResolveTargetFile
			}

		case MoveStmt:
			if stmt.ObjectVar == "" {
				return validateError(idx, "move requires input variable")
			}
			if kind, ok := objectKinds[stmt.ObjectVar]; ok && kind == ResolveTargetFolder {
				return validateError(idx, "move on folder is reserved in v1")
			}
			if stmt.OutputVar != "" {
				objectKinds[stmt.OutputVar] = ResolveTargetFile
			}

		case PatchStmt:
			if stmt.ObjectVar == "" {
				return validateError(idx, "patch requires input variable")
			}
			if kind, ok := objectKinds[stmt.ObjectVar]; ok && kind == ResolveTargetFolder {
				return validateError(idx, "patch does not support folder objects")
			}
			if stmt.OutputVar != "" {
				objectKinds[stmt.OutputVar] = ResolveTargetFile
			}

		case DeleteStmt:
			if stmt.DeleteAll {
				if len(stmt.Sources) == 0 {
					return validateError(idx, "delete all requires at least one source")
				}
				if stmt.DeleteAllKeepRoot {
					if len(stmt.Sources) != 1 {
						return validateError(idx, "delete all keep-root is only valid for a single source")
					}
					source := stmt.Sources[0]
					if source.ObjectVar != "" {
						return validateError(idx, "delete all keep-root form requires a path, not a variable")
					}
					if source.Path != "." && source.Path != "./" {
						return validateError(idx, "delete all keep-root is only valid for . or ./")
					}
				}
				for _, source := range stmt.Sources {
					if (source.ObjectVar == "" && source.Path == "") || (source.ObjectVar != "" && source.Path != "") {
						return validateError(idx, "delete all source must be exactly one of object variable or path")
					}
				}
				continue
			}
			if stmt.TargetKind != DeleteTargetFile && stmt.TargetKind != DeleteTargetFolder {
				return validateError(idx, "delete target must be file or folder")
			}
			if len(stmt.Sources) == 0 {
				return validateError(idx, "delete requires at least one source")
			}
			for _, source := range stmt.Sources {
				if (source.ObjectVar == "" && source.Path == "") || (source.ObjectVar != "" && source.Path != "") {
					return validateError(idx, "delete source must be exactly one of object variable or path")
				}
				if kind, ok := objectKinds[source.ObjectVar]; ok {
					if stmt.TargetKind == DeleteTargetFolder && kind != ResolveTargetFolder {
						return validateError(idx, "delete folder requires folder objects")
					}
					if stmt.TargetKind == DeleteTargetFile && kind == ResolveTargetFolder {
						return validateError(idx, "delete file does not accept folder objects")
					}
					objectKinds[source.ObjectVar] = kind
				}
			}

		case RenameStmt:
			if stmt.ObjectVar == "" {
				return validateError(idx, "rename requires input variable")
			}
			if stmt.OutputVar != "" {
				if kind, ok := objectKinds[stmt.ObjectVar]; ok {
					objectKinds[stmt.OutputVar] = kind
				}
			} else if _, ok := objectKinds[stmt.ObjectVar]; ok {
				// keep objectKinds intact for the source binding
			}

		case InvokeStmt:
			// OutputVar optional: omit to print result to stdout at runtime.

		case BatchInvokeStmt:
			if stmt.OutputVar == "" {
				return validateError(idx, "invoke concurrently requires output variable")
			}
			if stmt.OutputMode != "separate_files" && stmt.OutputMode != "single_file" {
				return validateError(idx, "invoke concurrently mode must be separate_files or single_file")
			}

		case SoftwareCallStmt:
			for _, flag := range stmt.Flags {
				if flag.Name == "" {
					return validateError(idx, "software flag name must not be empty")
				}
				if flag.Value.Kind != ValueKindString && flag.Value.Kind != ValueKindVariable {
					return validateError(idx, "software flag value must be a string literal or variable")
				}
				if flag.Value.Kind == ValueKindVariable && flag.Value.HasSelector {
					return validateError(idx, "software flag value does not support selectors")
				}
			}
		}
	}

	return nil
}

func validateError(instructionIndex int, msg string) error {
	return fmt.Errorf("satis validate error on instruction %d: %s", instructionIndex+1, msg)
}
