# Chunks and Plans: How They Are Edited, and Why They Are Designed This Way

Editing `Chunk` and `Plan` is not just a UI matter. It is about "how to structure the human work process."  
In one sentence: **Chunk answers "how to do this step," while Plan answers "which path to take next."**

The system maintains three planning levels: Intent, Plan, and Chunk. Once an Intent is set, it cannot be changed, and it gets a unique ID across the system. A Plan lives under an Intent and is a topology of connected execution units. Each execution unit is composed of execution statements (`Satis Internal Language`, SatisIL). Every Chunk and every Plan under the same Intent share the same ID lineage.

> Wilson's Note: From an engineering perspective, the current Plan/Chunk/Intent management and Workbench TUI are quite messy, because they were iterated step by step and produced through vibe coding. For UI, I still trust human aesthetics and custom tuning more; LLM cannot fully understand all my needs. But the current goal is rapid iteration and validating feasibility, so I did not optimize too much. In the future, both UI and Plan/Intent/Chunk management will be re-planned.

---

## 1) Separate editing objects by layer: local determinism, global growth

> Wilson's Note: Written by AI; I reviewed it and made additions.

The system splits editing into two layers:

- **Chunk layer (local)**: edit a node's `satis_text`, inputs/outputs, and handoff;
- **Plan layer (global)**: edit dependency edges, branches, entries, and cross-plan chaining.

The engineering judgment behind this is:  
inside a concrete action, the execution path is usually stable; but at the multi-step level, direction changes as new information arrives. At strategic scale, "move step by step" and "adapt to timing" are better policies. Do not fix the route too early, but once the route is set, execute decisively.

So I did not merge "local action" and "global route" into one giant script. I model them separately to reduce complexity coupling. At the Chunk level, paths are deterministic and scheduled in a strict DAG form. After a Chunk planning cycle, the system can selectively terminate the Chunk (planning done), continue to the next Plan (continue along existing route), or rerun.

---

## 2) How Chunk editing works: make each step a controllable atom

> Wilson's Note: Written by AI; I reviewed it and it is basically fine. This section is mainly for developers.

In Workbench, Chunk editing centers on three things:

- Edit body: directly edit `satis_text` (the executable content of the current step);
- Edit inputs: declare upstream dependencies via `handoff_inputs` (`from_step` + `from_port` + `var_name`);
- Edit type: switch between `task` and `decision` (`Ctrl+T`) while keeping `chunk_id` unchanged but switching semantics.

At the same time, the system enforces hard boundaries to prevent runaway single-step edits:

- Only leaf chunks can be deleted; root and last remaining node cannot;
- Half-filled handoff is rejected;
- `decision` branches are maintained by structured commands (`branch`, `decisiondefault`, etc.) to avoid JSON drift from manual edits.

This turns the intuition of "do things one step at a time" into machine-verifiable constraints:  
users can freely modify each step, but each step must be a fully defined, clearly input-bound, semantically explicit atomic operation.

---

## 3) How Plan editing works: turn "move-and-see" into explicit increments

Plan editing is not one-shot finalization. It is continuous revision of a growing graph. There are three typical incremental actions:

- **In-graph increment**: add child chunks, patch edges, edit branches so the existing graph grows;
- **Fragment increment**: use `attachfragment` to append new nodes/edges without replacing history;
- **Cross-plan increment**: use `plan-continue` / `plan-change` / `plan-detach` to attach, replace, or detach between parent and child plans.

These capabilities correspond to "move to what is visible first, then decide the next segment," rather than "compute the whole map before departure."  
Technically, the system internalizes "decide later" space into graph structure through navigation nodes like `Next Plan` / `Return Plan`.

Mechanistically, "strategic stability + tactical flexibility" relies on one loop:

- Strategic anchoring first: global semantic constraints like `intent_id`, `goal`, and `plan_id` keep the "why we fight" from drifting;
- Tactical maneuverability: `plan-change`, `attachfragment`, and `decision branch` allow local rerouting, but rerouting is explicit and traceable;
- Convergence as referee: `render/validate/submit/run` compresses temporary maneuvers back into unified constraints, avoiding long-term "guerrilla state."

> Some metaphorical references to *The Art of War* were added by AI here.

---

## 4) Why drafts are allowed first, then convergence is required: editing/runtime split

> Wilson's Note: Written by AI; I reviewed it and it is basically fine. If we get a better UI later, this part may be redesigned. UI is not the main contradiction for now, so I used a temporary Workbench TUI for reference only. Personally, I like Web UI a lot: browser rendering is elegant, operation is flexible, visuals are great. We may move in that direction later.

The system intentionally allows saving invalid drafts in Workbench, but enforces checks at `render` / `submit` / `run`.  
This is not quality relaxation; it separates two phases:

- **Exploration phase**: tolerate temporary imperfection to lower trial-and-error cost;
- **Execution phase**: strong convergence constraints (single entry, structural validity, resolvable dependencies) to guarantee runnability.

Mapped to human work style:

- Idea phase can be rough;
- Action phase must be rigorous.

So "move step by step" here is not arbitrariness. It is an engineering flow of **explore first, converge later**.

---

## 5) Key mechanism: extract "relations" from free text via handoff

> Wilson's Note: Also AI-written, and I reviewed it. I modified parts mainly for developers.

Many systems hide dependencies in natural language and end up relying on guesses.  
Here dependencies are made explicit: use handoff to derive `edges`, `depends_on`, and `entry_chunks`.

Benefits come in three layers:

- **Traceable**: you know exactly which step/port each input comes from;
- **Recomputable**: after handoff changes, topology can be rebuilt automatically;
- **Diagnosable**: dependency mistakes surface as structural issues, not hidden runtime failures.

In short, the system upgrades "I feel this step depends on the previous one" into "I explicitly declare and verify this dependency."

---

## 6) Two seemingly conflicting premises, unified in one philosophy

> Wilson's Note: AI-written with deletions/edits by me.

You raised two points:

1. At detailed execution level, people work one step at a time, and each step's internal path is mostly fixed;  
2. At practical strategy level, people also "move and decide along the way."  
3. Conceptually, the system decouples planning thought from concrete execution steps, so points 1 and 2 must both be embraced.

This Chunk/Plan design unifies them by:

- **Pursuing determinism inside Chunk**: make each step executable, reproducible, and replaceable;
- **Accommodating uncertainty in Plan**: represent route changes as incremental graph edits and explicit branching;
- **Unifying convergence before run**: use validation, normalization, and event observation to compress exploration into executable facts.

If described more directly, this is two-level control:

- **Level-1 control**: entry consistency, solvable dependencies, structural validity are non-negotiable red lines;
- **Level-2 control**: node add/remove/edit, branch redirection, and cross-plan attachment are freedoms within those red lines.

Overall, the design of Chunks and Plans uses local determinism to hold global uncertainty, then continuously converges uncertainty into the next deterministic step.

---

## 7) Practical recommendations for developers (short version)

- Treat each Chunk as a minimum deliverable action before discussing full orchestration;
- Prefer handoff for dependencies; do not hide critical relations in body text;
- For major route changes, prioritize incremental methods like fragment/plan-continue instead of rewriting the whole graph;
- After each iteration, run `render -> run -> events/inspect` so structure and reality stay aligned.

When you work in this rhythm, Plan is no longer a static flowchart. It becomes an evolvable "work reasoning record."

> Wilson's Note: This reasoning record is auditable and dynamically extensible. To me it is beautiful: a three-in-one unity of "stable strategic direction, strict control-flow execution at the minimum action unit, and tactical move-and-see."
