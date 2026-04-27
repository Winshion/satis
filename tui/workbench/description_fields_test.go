package workbench

import (
	"strings"
	"testing"
)

func TestScaffoldPlanProvidesDescriptions(t *testing.T) {
	plan := ScaffoldPlan("/demo/project", "整理演示项目的执行意图")
	if strings.TrimSpace(plan.IntentDescription) == "" {
		t.Fatalf("expected scaffold intent description")
	}
	if strings.TrimSpace(plan.PlanDescription) != "" {
		t.Fatalf("expected empty scaffold plan description until user fills it")
	}
	if len(plan.Chunks) != 1 {
		t.Fatalf("expected one scaffold chunk, got %d", len(plan.Chunks))
	}
	if strings.TrimSpace(plan.Chunks[0].Description) == "" {
		t.Fatalf("expected scaffold chunk description")
	}
	if !strings.Contains(plan.Chunks[0].Source.SatisText, "\ndescription: ") {
		t.Fatalf("expected scaffold chunk header description, got %q", plan.Chunks[0].Source.SatisText)
	}
}

func TestSetPlanLevelDescriptions(t *testing.T) {
	model := testWorkbenchModel()
	if err := model.SetIntentDescription("整理用户意图并准备执行"); err != nil {
		t.Fatalf("SetIntentDescription: %v", err)
	}
	if err := model.SetPlanDescription("先检查输入，再执行核心任务"); err != nil {
		t.Fatalf("SetPlanDescription: %v", err)
	}
	if got := model.Plan.IntentDescription; got != "整理用户意图并准备执行" {
		t.Fatalf("unexpected intent description %q", got)
	}
	if got := model.Plan.PlanDescription; got != "先检查输入，再执行核心任务" {
		t.Fatalf("unexpected plan description %q", got)
	}
}

func TestParsePlanDocumentSyncsChunkDescriptionFromHeader(t *testing.T) {
	plan, err := ParsePlanDocument(`{
  "protocol_version": 1,
  "plan_id": "plan_sync",
  "intent_id": "intent_sync",
  "intent_description": "完成同步测试意图",
  "plan_description": "验证 chunk 描述与头同步",
  "goal": "sync test",
  "entry_chunks": ["CHK_ROOT"],
  "chunks": [
    {
      "chunk_id": "CHK_ROOT",
      "kind": "task",
      "source": {
        "format": "satis_v1",
        "satis_text": "chunk_id: CHK_ROOT\nintent_uid: intent_sync\ndescription: 从 chunk head 读取描述\nchunk_port: port_root\n\nPwd\n"
      }
    }
  ],
  "edges": []
}`)
	if err != nil {
		t.Fatalf("ParsePlanDocument: %v", err)
	}
	if len(plan.Chunks) != 1 {
		t.Fatalf("expected one chunk, got %d", len(plan.Chunks))
	}
	if got := plan.Chunks[0].Description; got != "从 chunk head 读取描述" {
		t.Fatalf("unexpected synced description %q", got)
	}
}

func TestParsePlanRejectsMissingDescriptions(t *testing.T) {
	_, err := ParsePlan(`{
  "protocol_version": 1,
  "plan_id": "plan_invalid",
  "intent_id": "intent_invalid",
  "goal": "invalid",
  "entry_chunks": ["CHK_ROOT"],
  "chunks": [
    {
      "chunk_id": "CHK_ROOT",
      "kind": "task",
      "source": {
        "format": "satis_v1",
        "satis_text": "chunk_id: CHK_ROOT\nintent_uid: intent_invalid\nchunk_port: port_root\n\nPwd\n"
      }
    }
  ],
  "edges": []
}`)
	if err == nil {
		t.Fatalf("expected ParsePlan to reject missing descriptions")
	}
	if !strings.Contains(err.Error(), "intent_description") {
		t.Fatalf("expected missing description error, got %v", err)
	}
}
