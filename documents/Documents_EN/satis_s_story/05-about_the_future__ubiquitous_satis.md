# On the Future: The "Ubiquitous Idea"

> I wrote five documents in one day and got tired. This chapter is mostly my imagination. I only gave a prompt to LLM and barely revised it, just partial edits, for reference only.
>
> My prompt was:

```
Explain clearly in this document the concept of LLM Server, and why LLM can be applied across industries:
documents/Documents_ZH/satis的故事/05-关于未来的「泛念」.md

1. The Satis system itself was initially designed toward an Agent System. In the future, we can embed an Agent and let LLM manage the Satis system.
(1) satisil follows control-flow statements similar to natural-language instructions
(2) satis supports maintaining multiple context-carrying conversations in the system kernel (but currently has no context management)
(3) the system has already reserved mechanism-level design for future "LLM outputs compliant Satis Chunk statements and Plan"
2. In the future, user terminals may only keep the satis system; users register software to this satis system, then connect to an LLM Server (like connecting to WiFi), and can perform all kinds of thinking exercises.
3. Briefly write some future satis application scenarios you believe in.
```

This chapter discusses a more "infrastructure-level" question:  
if we place `satis` on a longer future timescale, what might it become?

My view is:  
many future terminals may not need all capabilities built in. They may need a new connection model: like connecting to WiFi today, they connect to a callable, switchable, governable **LLM Server**.

---

## 1) What is an LLM Server

In my context, LLM Server is not "one specific vendor API." It is a class of general infrastructure:

- It externally provides model inference, tool calling, contextual conversations, strategy configuration, and related capabilities;
- It internally manages model routing, permissions, security, billing, audit, and resource scheduling;
- It lets terminals avoid carrying all intelligence locally, and instead access intelligence on demand.

You can think of it as "the power grid of the intelligence era":  
terminals do not need to generate electricity themselves, but they need stable, secure, metered interfaces for using it.

---

## 2) Why LLM can be applied across industries

Many technologies verticalize. LLM has a special property:  
it does not process only one task type, but **mapping and organization in symbol systems**.  
As long as an industry has representable objects such as text, images, rules, workflows, and decision explanations, LLM can participate.

Its cross-industry capability does not come from "knowing everything," but from three general abilities:

- **Semantic alignment**: translate human intent into machine-executable actions;
- **Structural induction**: extract patterns and build intermediate representations from incomplete information;
- **Interactive iteration**: continuously correct outputs in a feedback loop rather than ending at one-shot computation.

So from research, healthcare, and manufacturing to governance, education, and finance, the key differences are mostly in constraints and accountability boundaries, not in whether LLM can be used.

---

## 3) `satis` and Agent System: why it is naturally on this path

`satis` was designed toward Agent System from the beginning; this is not an after-the-fact label.  
It already has three mechanism-level foundations:

1. **SatisIL uses a control-flow style close to natural language**  
   It is not a purely symbolic low-level DSL. It is closer to "human-readable, machine-executable" action syntax. This gives LLM a natural interface for generating executable instructions. In my practice, without any satis-specific pretraining (in fact I have not pretrained any LLM), most provider LLMs can produce correct SatisIL control-flow statements once given correct syntax descriptions.

2. **The kernel supports multi-conversation contextual states**  
   The system can already maintain multiple contextual conversation traces at execution layer. Context management is still not complete, which is exactly a key engineering point for the next stage. For context management, however, I suspect it may be solved through an unconventional approach.

3. **Mechanisms are reserved for "LLM outputs Chunk/Plan"**  
   Through `Chunk + Plan`, handoff, validation, normalization, and runtime observation, the system already has a foundation to receive structured outputs from LLM.  
   In other words, the future is not "let LLM write scripts freely," but "let LLM produce Chunk and Plan under auditable constraints."

---

## 4) One possible future terminal form

Future user terminals may become very lightweight:

- Keep only an execution-and-governance kernel like `satis` locally;
- Let users register domain software into the system (forming a stable capability catalog), and also download packaged software from server-side providers;
- Connect to an LLM Server (choose endpoint, authentication, billing, strategy like WiFi connection);
- Complete complex task orchestration, validation, and review in one unified environment.

This brings a practical shift: terminals are no longer judged by "how many apps are installed," but by "how much intelligent capability can be connected, and whether it is governable."

---

## 5) Why this route is worth doing

Because it tries to satisfy three goals that often conflict:

- **Flexibility**: LLM brings high adaptability and maneuverable strategy generation;
- **Stability**: `satis` keeps order through structured execution and validation;
- **Auditability**: long-horizon processes become traceable, reviewable, and correctable.

Without an execution kernel, LLM tends to become "good at talking, weak at landing."  
Without LLM, an execution kernel may become "good at execution, weak in strategic elasticity."  
Only by connecting both can we form truly sustainable Agent-system engineering.

---

## 6) Future application scenarios for `satis` (brief)

- **Research and AI4S**: multi-evidence integration, experiment orchestration, hypothesis-verification traceability;
- **Embodied intelligence/robotics**: task decomposition, strategy rerouting, failure rollback, and experience accumulation;
- **Enterprise knowledge and operation automation**: cross-system data collection, rule execution, and exception-branch handling;
- **Education and training**: structuring the process of thinking, not just final answers;
- **Medical and compliance-assisted workflows**: human-AI collaboration chains emphasizing auditability, accountability, and interpretability.

It may not become the main system in every industry, but it has the potential to become an "intelligent process substrate."

---

## 7) One closing sentence

I prefer to see `satis` as an experiment in a "connection layer":

- Downward, connect stable software capabilities; upward, connect continuously evolving LLM capabilities.  
- If this connection layer becomes robust enough, future "thinking exercises" will no longer be just chatting, but executable, verifiable, and accumulable system capability.