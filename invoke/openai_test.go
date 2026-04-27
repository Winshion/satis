package invoke

import (
	"strings"
	"testing"
)

func TestExtractTextResponseRejectsReasoningOnlyTruncation(t *testing.T) {
	resp := []byte(`{
	  "choices": [
	    {
	      "finish_reason": "length",
	      "message": {
	        "content": "",
	        "role": "assistant",
	        "reasoning_content": "long reasoning without final answer"
	      }
	    }
	  ]
	}`)
	_, err := extractTextResponse(resp)
	if err == nil {
		t.Fatalf("expected error for reasoning-only truncation")
	}
	if !strings.Contains(err.Error(), "reasoning-only output") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractTextResponsePrefersNormalContent(t *testing.T) {
	resp := []byte(`{
	  "choices": [
	    {
	      "finish_reason": "stop",
	      "message": {
	        "content": "hello",
	        "role": "assistant",
	        "reasoning_content": "reasoning"
	      }
	    }
	  ]
	}`)
	got, err := extractTextResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Fatalf("expected hello, got %q", got)
	}
}
