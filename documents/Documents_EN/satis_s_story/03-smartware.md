# Smartware: A Paradigm Shift from "Reusing Software" to "Reusing Thinking"

> Wilson's Note: Although this document section was written by AI, this is actually the core of my conception of the satis system. It came from a discussion in my research group where someone said an "LLM" could be treated as "hardware." But in fact, LLM is software, right? So I started thinking: software itself can also be treated like "hardware," as a reusable component. Then what is the new layer above it? It is thinking trajectories. From my perspective, current harness engineering still **does not decouple execution flow from thinking flow**.
>
> During this period, tools like OpenClaw, CodeX, and Claude Code kept emerging. While using them, I suddenly felt how elegant the SKILLS design is. SKILLS is a primitive form of smartware, but its constraints are too loose, which makes LLM prone to wasting too many tokens. Maybe its better to unify interfaces of all software, and ask software developers to reserve one interface for satis. Then if satis is connected to an Agent framework in the future (not implemented yet; though I believe this part is manageable, I still do not think one person can beat big-company collective force), software can be called legally and simply. As for how software is implemented, leave that to software developers. I believe software developers can build excellent software. Satis only needs to be like Socrates: apply its internal intelligence to orchestrate those software tools through simple interfaces.
>
> Satis is not an Agentic System, but in practice it can perform Agentic-System-like functions. For tasks like deep scientific research, I think results can be better with satis than with a generic Agentic System, because research usually involves many tools. In the future, tools will only increase. If each tool needs massive Agentic RL or post-training for standardization, GPU time will explode. During inference, one wrong tool use can cost huge correction time and token blowup. But if software usage is abstracted as "tools," and those tools are abstracted well enough with unified and easy interfaces, maybe we can solve more problems with fewer control-flow steps.
>
> Looking from the other side: if there is an LLM that can quickly master smartware usage, software engineers and smartware engineers can be separated. Software engineers focus on building software that conforms to the Satis interface. Smartware engineers focus on quickly and accurately querying tool usage and calling tools under real conditions. Then control flow and thinking flow are separated, and this problem becomes much easier to handle.

For decades, software engineering had a default premise:  
**software is the interface between humans and hardware, and also the main reusable unit.**

This premise is not wrong, but it has a limit:  
we reuse "what can be done" (functions), yet we can hardly reuse "why this path," "under what conditions," and "how to reroute after failure" (thinking).

What `satis` tries to change is exactly this layer.  
Here, software returns to being a stable compute unit, while the reusable core is elevated to a **thinking unit**, which I call **smartware**.

---

## 1) Why this is a paradigm shift, not just a new term

Traditional software reuse emphasizes function/module/service reuse, essentially **operation reuse**.  
In complex tasks, however, what is truly scarce is often not "how to call one API," but:

- Which step first, which step next;
- On what basis to branch when ambiguity appears;
- When to explore and when to converge;
- How to crystallize experience into a reusable path for next time.

So I introduced one key separation:

- **Software**: fixed, callable, verifiable compute capability;
- **Smartware**: reusable thinking that organizes steps, branches, constraints, and feedback around goals.

In short, software answers "can it be computed," smartware answers "how to compute it correctly."

---

## 2) In `satis`, smartware is not abstract rhetoric; it has concrete carriers

This idea is not just a slogan in docs. It is already grounded in mechanisms:

- `Chunk + Plan` separates local execution from global orchestration;
- `task / decision / planning` enables switching between deterministic execution and dynamic branching;
- `handoff -> edges/depends_on/entry_chunks` elevates dependencies from natural language to computable structure;
- `render/validate/submit/run` forms an engineering loop: drafts can explore, runtime must converge.

So smartware is neither a prompt snippet nor a script fragment.  
It is a **thinking structure that is editable, verifiable, executable, and reviewable**.

---

## 3) Software's role in the new paradigm: from lead actor to replaceable component

Current software mechanisms in code already reflect this change:

- unified registration and indexing through `SoftwareRegistry`;
- function and argument constraints declared in `forsatis.json`;
- semantic intent and purpose declared in `SKILL.md` frontmatter (`name/description`);
- recursive `Software refresh` that rebuilds `SKILLS.md` indexes to keep registry and docs in sync;
- runtime calls standardized through `SoftwareCallStmt` to `python_script` / `binary` runners.

This means:  
software capability is a standardized, replaceable operator layer; smartware is a goal-oriented decision layer on top.  
When software versions change, smartware remains reusable as long as interface contracts hold. When goals change, smartware adapts without rewriting all software.

---

## 4) What exactly is reused in "thinking reuse"

Smartware does not reuse lines of code. It reuses four higher-value assets:

- **Goal decomposition patterns**: how to split vague goals into executable stages;
- **Decision logic**: when to choose branch A vs branch B (`decision`);
- **Convergence strategies**: when drafts are acceptable and when strict checks are mandatory;
- **Failure-handling experience**: rollback/retry/reroute path patterns after failure.

In traditional systems, these assets are scattered in verbal know-how, comments, and temporary chats.  
In `satis`, they are fixed into plan graphs, handoff relations, runtime events, and audit records, so they can be reused across people and tasks.

> This framework was originally intended for AI-for-Science Deep Research. In practice, Agent systems struggle to decouple thinking from implementation. My argument may not be complete, but at least in my field, this direction feels reasonable.

---

## 5) Boundary between smartware and software: avoid the illusion of "everything is intelligent"

This design is intentionally restrained:

- Software is for **stable execution**, emphasizing determinism, testability, and clear parameter contracts;
- Smartware is for **strategy organization**, allowing probing, branching, and context-driven adjustment;
- The two connect through structured interfaces, not through mutual intrusion.

This boundary is critical.  
Hardcode thinking into software, and the system loses maneuverability.  
Outsource all software behavior to ad-hoc improvisation, and the system loses reliability.

---

## 6) A practical conclusion: what migrates in the future is the "smartware library"

Under this paradigm, the truly compounding organizational asset is no longer only a software repository, but:

- an evolving library of smartware templates;
- an auditable set of decision paths;
- validated closed loops of "goal -> route -> execution -> feedback."

Software is still necessary, but more like a standardized toolbox.  
Smartware is the reusable unit of task intelligence.

From an engineering perspective, this is the change I want `satis` to push:  
**move reuse focus from function components to thinking components, so the system accumulates methods of solving problems.**
