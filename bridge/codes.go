package bridge

// Validation and structured error codes (stable strings for clients).
const (
	CodeValidationNilPlan              = "VALIDATION_NIL_PLAN"
	CodeValidationDuplicateChunkID     = "VALIDATION_DUPLICATE_CHUNK_ID"
	CodeValidationUnknownChunkRef      = "VALIDATION_UNKNOWN_CHUNK_REF"
	CodeValidationSelfLoop             = "VALIDATION_SELF_LOOP"
	CodeValidationDAGCycle             = "VALIDATION_DAG_CYCLE"
	CodeValidationBadEntry             = "VALIDATION_BAD_ENTRY_CHUNK"
	CodeValidationUnreachable          = "VALIDATION_UNREACHABLE_CHUNK"
	CodeValidationDependsMismatch      = "VALIDATION_DEPENDS_EDGE_MISMATCH"
	CodeValidationSatisParse           = "VALIDATION_SATIS_PARSE"
	CodeValidationSatisValidate        = "VALIDATION_SATIS_VALIDATE"
	CodeValidationChunkIDMismatch      = "VALIDATION_CHUNK_ID_MISMATCH"
	CodeValidationInputBinding         = "VALIDATION_INPUT_BINDING_MISMATCH"
	CodeValidationRepeat               = "VALIDATION_REPEAT_CONFIG"
	CodeValidationUnsupportedChunkKind = "VALIDATION_UNSUPPORTED_CHUNK_KIND"
	CodeValidationDecisionConfig       = "VALIDATION_DECISION_CONFIG"
	CodeValidationLoopPolicy           = "VALIDATION_LOOP_POLICY"
)

// Runtime / scheduler StructuredError codes (gopyd execution path).
const (
	CodeRunCancelled            = "RUN_CANCELLED"
	CodeChunkExecutionFailed    = "CHUNK_EXECUTION_FAILED"
	CodeRunPartialFailure       = "RUN_PARTIAL_FAILURE"
	CodeChunkTimeout            = "CHUNK_TIMEOUT"
	CodeDecisionExecutionFailed = "DECISION_EXECUTION_FAILED"
	CodeDecisionLoopLimitExceeded = "DECISION_LOOP_LIMIT_EXCEEDED"
)

// ErrorGroup returns a coarse bucket for metrics/UI: "validation", "runtime", or "unknown".
func ErrorGroup(code string) string {
	switch code {
	case CodeValidationNilPlan, CodeValidationDuplicateChunkID, CodeValidationUnknownChunkRef,
		CodeValidationSelfLoop, CodeValidationDAGCycle, CodeValidationBadEntry,
		CodeValidationUnreachable, CodeValidationDependsMismatch, CodeValidationSatisParse,
		CodeValidationSatisValidate, CodeValidationChunkIDMismatch, CodeValidationInputBinding,
		CodeValidationRepeat, CodeValidationUnsupportedChunkKind, CodeValidationDecisionConfig,
		CodeValidationLoopPolicy:
		return "validation"
	case CodeRunCancelled, CodeChunkExecutionFailed, CodeRunPartialFailure, CodeChunkTimeout,
		CodeDecisionExecutionFailed, CodeDecisionLoopLimitExceeded:
		return "runtime"
	default:
		return "unknown"
	}
}

// FailureLayerForStructuredError returns a stable observation bucket for failures.
func FailureLayerForStructuredError(err *StructuredError) string {
	if err == nil {
		return "unknown"
	}
	switch err.Stage {
	case StagePlanning:
		return "planning_continuation"
	case StageValidation:
		return "gopyd_validation"
	case StageScheduling:
		return "gopyd_scheduling"
	case StageExecution, StageVFS, StageInvoke:
		return "runtime_execution"
	default:
		if ErrorGroup(err.Code) == "runtime" {
			return "runtime_execution"
		}
		if ErrorGroup(err.Code) == "validation" {
			return "gopyd_validation"
		}
		return "unknown"
	}
}
