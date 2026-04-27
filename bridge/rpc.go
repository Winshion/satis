package bridge

// JSON-RPC 2.0 method names (v1). Transport is intentionally not fixed here.
const (
	RPCSubmitChunkGraph = "gopy.submit_chunk_graph"
	RPCStartRun         = "gopy.start_run"
	RPCPauseRun         = "gopy.pause_run"
	RPCResumeRun        = "gopy.resume_run"
	RPCCancelRun        = "gopy.cancel_run"
	RPCGetRun           = "gopy.get_run"
	RPCStreamRunEvents  = "gopy.stream_run_events"
	RPCInspectRun       = "gopy.inspect_run"
	RPCInspectChunk     = "gopy.inspect_chunk"
	RPCInspectObject    = "gopy.inspect_object"
	RPCListArtifacts    = "gopy.list_artifacts"
)
