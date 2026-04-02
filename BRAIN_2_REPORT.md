# Brain 2.0 — Architecture Review & Test Strategy

> Analysis date: 2026-04-02
> Scope: Current Brain v1 system, what to learn, what to test, how to silo it

---

## 1. What We Learned from Brain v1

### What Works Well

| Strength | Why It Matters |
|----------|----------------|
| **Dual-layer memory** | Rule-based extraction (zero cost, instant) catches 80% of structured facts. LLM extraction (batched every 30 msgs) catches nuance. Neither alone is sufficient. |
| **Context layering** | System prompt is assembled from 9 layers (SOUL → memories → skills → knowledge → channel context). This gives the Brain genuine workspace awareness, not just chat history. |
| **Thread vs. channel attention** | Different context budgets for focused threads (5K knowledge cap) vs. broad channels (full cross-channel awareness). This prevents context pollution. |
| **Agent delegation** | Brain can hand off to specialized agents with their own tools/personality. Single-pass delegation avoids infinite loops. |
| **ResultAsAnswer optimization** | Tools like `create_document` skip Round 2 entirely. Saves one LLM call for simple tool actions. |
| **Model-agnostic routing** | Works with OpenRouter, Gemini, Grok, Ollama, OpenAI. Not locked to any provider. |
| **Memory consolidation** | 6-hour cycle that supersedes stale memories, generates insights, prevents bloat. Self-maintaining. |
| **Staleness filtering** | Drops mentions older than 5-10 minutes. Prevents Brain from responding to stale backlog. |

### What's Limited

| Limitation | Impact | Root Cause |
|------------|--------|------------|
| **2-round max** | Brain can't iterate on complex tasks. If Round 2 response is wrong, there's no Round 3. | Hardcoded loop limit |
| **Sequential tool execution** | 3 tool calls = 3x latency. `web_search` + `search_knowledge` + `list_tasks` runs in series, not parallel. | Simple `for` loop, no goroutines |
| **No streaming** | User waits 5-15 seconds seeing nothing. `CompleteStream` exists but Brain never uses it. | Architecture assumes full-response model |
| **All tools, all the time** | Brain sees every tool (20+ built-in + all MCP). No scoping by context. A "what time is it?" query gets the full tool catalog. | No tool filtering logic |
| **Fixed token budget** | MaxTokens=2048 regardless of model. Claude/GPT-4 can output 8K+ tokens. Responses get truncated. | Hardcoded constant |
| **No tool timeout** | A hanging MCP tool blocks the entire Brain response forever. | No `context.WithTimeout` |
| **No per-user rate limit** | One user can saturate all 16 goroutine slots. | Global semaphore only |
| **Memory is query-filtered only** | Important memories can be missed if user's message doesn't semantically match. No "pinned" always-include memories. | Single retrieval strategy |

### What's Hardcoded That Shouldn't Be

| Parameter | Current | Should Be |
|-----------|---------|-----------|
| MaxTokens | 2048 | Per-model (e.g., 4096 for GPT-4, 8192 for Claude) |
| Temperature | 0.7 | Per-workspace or per-task (lower for facts, higher for creative) |
| Tool-calling rounds | 2 | Configurable, or dynamic (keep going until Brain says "done") |
| Context window | 40 messages | Adaptive based on model's context length |
| Concurrency | 16 goroutines | Configurable via env var |
| System prompt cap | 100K chars | Dynamic based on model |

---

## 2. Brain 2.0 — What to Test

### The Core Question

Brain v1 is a **single-shot reasoner**: trigger → assemble context → 2 LLM calls → done.

Brain 2.0 should be a **pipeline orchestrator**: trigger → plan → execute steps → reflect → respond.

### Three Architectures to Consider

#### A. Hermes Pipeline (Recommended for Testing)

Inspired by Hermes function-calling: **Plan → Act → Observe → Repeat**.

```
User message
  ↓
PLANNER (fast model, low tokens)
  → Decides: what tools to call, in what order, what info is needed
  → Output: structured plan (JSON array of steps)
  ↓
EXECUTOR (parallel where possible)
  → Runs planned tools concurrently
  → Collects results
  ↓
SYNTHESIZER (main model, high tokens)
  → Receives: user message + plan + all results
  → Produces: final response
  ↓
REFLECTOR (optional, async)
  → Did the response address the user's need?
  → Should we save any memories?
  → Any follow-up actions?
```

**Why this is better:**
- Planner can use a cheap/fast model (gpt-4o-mini, gemini-flash)
- Tools run in parallel (3x-5x faster)
- Synthesizer gets clean, structured input
- Reflector runs async (doesn't block response)

**Why test this:**
- Direct comparison: same prompts, v1 (sequential 2-round) vs v2 (pipeline)
- Measure: latency, tool accuracy, response quality, cost

#### B. ReAct Loop (Agentic)

Classic ReAct: **Thought → Action → Observation → Thought → ... → Answer**.

```
while not done:
  thought = LLM("Given context, what should I do next?")
  if thought == "I have enough info":
    break
  action = LLM("Which tool to call?")
  observation = execute(action)
  context.append(observation)
answer = LLM("Synthesize final answer")
```

**Pros:** More flexible, can iterate until satisfied.
**Cons:** Unpredictable cost (could loop 10+ times), harder to test deterministically, higher latency.

**Verdict:** Good for agents, too unpredictable for Brain (which should be fast and reliable).

#### C. DAG Execution (Advanced)

Tools as a directed acyclic graph. Planner outputs a dependency graph, executor runs in topological order with max parallelism.

```
search_web ──┐
              ├──→ synthesize
search_kb  ──┘
list_tasks ──────→ format_tasks
```

**Pros:** Maximum parallelism, explicit dependencies.
**Cons:** Complex to implement, over-engineered for most queries.

**Verdict:** Future optimization. Not worth testing first.

### Recommendation: Test Architecture A (Hermes Pipeline)

---

## 3. How to Silo the Test

### Principle: Zero Risk to v1

Brain 2.0 runs as a **completely separate code path**. No shared functions, no shared state, no shared configuration. v1 continues to work exactly as-is.

### Silo Strategy

```
internal/
├── brain/              ← v1 (UNTOUCHED)
│   ├── brain.go
│   ├── openrouter.go
│   └── memory.go
├── brain2/             ← v2 (NEW DIRECTORY)
│   ├── pipeline.go     ← Planner → Executor → Synthesizer
│   ├── planner.go      ← Fast model, outputs step plan
│   ├── executor.go     ← Parallel tool runner
│   ├── synthesizer.go  ← Final response generation
│   └── reflector.go    ← Async memory/follow-up (optional)
└── server/
    ├── brain.go         ← v1 handler (UNTOUCHED)
    ├── brain_tools.go   ← v1 tools (UNTOUCHED)
    └── brain2.go        ← v2 handler (NEW FILE)
```

### Activation

**Feature flag** — workspace-level setting:

```go
// brain_settings table
brain_version TEXT NOT NULL DEFAULT 'v1'  -- 'v1' or 'v2'
```

Toggle in Brain settings UI: "Brain Engine: v1 (Classic) | v2 (Pipeline) [Beta]"

When `brain_version == 'v2'`, the trigger in `ws.go` routes to `handleBrainV2` instead of `handleBrainMentionWithTools`.

**Rollback:** Change setting back to `v1`. Instant, per-workspace, no deploy needed.

---

## 4. What to Test & How

### Test Silo Size

**One workspace** is sufficient for initial testing. The silo is:
- 1 workspace with `brain_version = 'v2'`
- Same tools, same memories, same knowledge base
- Side-by-side: run same prompts in v1 workspace and v2 workspace

### Test Matrix

| Test Case | What We Measure | v1 Behavior | v2 Expected |
|-----------|----------------|-------------|-------------|
| **Simple question** ("what tasks are overdue?") | Latency, accuracy | 2 LLM calls, sequential | 1 planner + 1 tool + 1 synthesizer, faster |
| **Multi-tool query** ("search web for X and check our knowledge base") | Latency, parallelism | 2 tools sequential + 2 LLM calls | 2 tools parallel + 2 LLM calls, ~2x faster |
| **Creative request** ("write a weekly report") | Quality, token usage | 2048 max tokens, may truncate | Dynamic MaxTokens based on model |
| **Tool-heavy task** ("create a task, add to calendar, notify team") | Execution order, errors | Sequential, 2-round limit | Planned sequence, parallel where safe |
| **No-tool question** ("explain our pricing strategy") | Response time, context use | Still makes 1 LLM call (no tools), fast | Planner detects no tools needed, skips executor |
| **Delegation** ("ask the researcher agent about X") | Handoff quality | Single delegation call | Planner includes delegation as a step |
| **Memory recall** ("what did we decide about the API last week?") | Recall accuracy | Query-filtered memories | Same + planner can call recall_memory explicitly |
| **Error recovery** ("search for X" but tool fails) | Graceful handling | Tool returns error string, Round 2 includes it | Executor catches error, Synthesizer acknowledges |

### Metrics to Capture

```go
type BrainMetrics struct {
    Version       string        // "v1" or "v2"
    TotalLatency  time.Duration // User message → final response
    PlanLatency   time.Duration // v2 only: planner time
    ToolLatency   time.Duration // Total tool execution time
    SynthLatency  time.Duration // Final synthesis time
    LLMCalls      int           // Number of LLM API calls
    ToolCalls     int           // Number of tool executions
    InputTokens   int           // Total input tokens
    OutputTokens  int           // Total output tokens
    CostUSD       float64       // Estimated cost
    Model         string        // Primary model used
    PlannerModel  string        // v2 only: planner model
    Success       bool          // Did it complete without error
    ToolsParallel int           // v2 only: max concurrent tools
}
```

Log to `brain_action_log` table (already exists) with version tag.

### A/B Test Protocol

1. Create two workspaces: `test-v1` and `test-v2`
2. Same members, same channels, same knowledge base, same memories
3. Run identical prompts in both (scripted or manual)
4. Compare metrics side-by-side
5. Review response quality manually (is v2 actually better, or just faster?)

---

## 5. Brain 2.0 Pipeline — Detailed Design

### 5.1 Planner

**Model:** Fast, cheap (gemini-2.0-flash-lite, gpt-4o-mini, or grok-4.1-fast)
**MaxTokens:** 512 (plan is small)
**Temperature:** 0.3 (deterministic plans)

**Input:**
```
System: You are a task planner. Given a user message and available tools,
output a JSON plan. Each step has: tool_name, arguments, depends_on (step IDs).
Steps with no dependencies can run in parallel.
If no tools needed, output: {"steps": [], "direct_answer": true}

Tools: [tool catalog - names + descriptions only, no full schemas]
User: {message}
Context: {recent 5 messages for conversation context}
```

**Output:**
```json
{
  "steps": [
    {"id": "s1", "tool": "search_workspace", "args": {"query": "overdue tasks"}, "depends_on": []},
    {"id": "s2", "tool": "list_tasks", "args": {"status": "in_progress"}, "depends_on": []},
    {"id": "s3", "tool": "web_search", "args": {"query": "project management best practices"}, "depends_on": []}
  ],
  "direct_answer": false
}
```

**Cost:** ~$0.001 per plan (flash model, 512 output tokens)

### 5.2 Executor

**Parallel execution** with dependency resolution:

```go
func (e *Executor) Run(plan Plan, tools ToolRegistry) []StepResult {
    completed := map[string]StepResult{}
    pending := plan.Steps

    for len(pending) > 0 {
        // Find steps with all dependencies met
        ready := filterReady(pending, completed)

        // Execute ready steps in parallel
        var wg sync.WaitGroup
        results := make([]StepResult, len(ready))
        for i, step := range ready {
            wg.Add(1)
            go func(i int, s Step) {
                defer wg.Done()
                ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
                defer cancel()
                results[i] = tools.Execute(ctx, s)
            }(i, step)
        }
        wg.Wait()

        // Move to completed
        for i, step := range ready {
            completed[step.ID] = results[i]
        }
        pending = filterPending(pending, completed)
    }
    return allResults(completed)
}
```

**Key improvement over v1:** 30-second timeout per tool. v1 has no timeout.

### 5.3 Synthesizer

**Model:** Same as workspace Brain model (user's choice)
**MaxTokens:** Dynamic based on model (2048 for small, 4096 for large, 8192 for Claude/GPT-4)
**Temperature:** 0.7 (same as v1)

**Input:**
```
System: {full system prompt from v1 — SOUL, memories, skills, knowledge, etc.}
User: {original message}
Plan: {what tools were called and why}
Results: {tool results, formatted}
```

**Output:** Final response to user.

### 5.4 Reflector (Async)

Runs in a goroutine after response is sent. No user-facing latency.

```go
go func() {
    // 1. Should we save any memories from this interaction?
    // 2. Did we answer the question? (self-check)
    // 3. Any follow-up actions? (e.g., "remind me tomorrow")
}()
```

**Model:** Cheapest available (same as memory extraction model)
**Cost:** ~$0.001 per reflection

---

## 6. Implementation Phases

### Phase 1: Scaffold (1 commit)
- Create `internal/brain2/` directory
- `pipeline.go` with Planner → Executor → Synthesizer skeleton
- `brain2.go` handler in server
- Feature flag in brain_settings migration
- Route toggle in ws.go
- **No actual LLM calls yet** — just the wiring

### Phase 2: Planner (1 commit)
- Implement planner with fast model
- Tool catalog formatting (names + descriptions)
- Plan JSON parsing
- "direct_answer" short-circuit (skip executor for no-tool queries)

### Phase 3: Parallel Executor (1 commit)
- Reuse existing tool implementations from `brain_tools.go`
- Add `context.WithTimeout` (30s per tool)
- Parallel execution with dependency resolution
- Metrics logging

### Phase 4: Synthesizer + Integration (1 commit)
- Full system prompt assembly (reuse v1's `BuildSystemPrompt`)
- Dynamic MaxTokens per model
- Wire everything together
- UI toggle in Brain settings

### Phase 5: Evaluate (no code)
- Run A/B test protocol
- Compare metrics
- Decide: ship v2 as default, keep as option, or iterate

---

## 7. Risk Assessment

| Risk | Mitigation |
|------|-----------|
| v2 breaks v1 | Completely separate code path, feature flag, instant rollback |
| Planner makes bad plans | Planner errors fall back to v1 behavior (2-round) |
| Parallel tools cause race conditions | Tools are stateless functions, no shared mutable state |
| Higher cost (3 LLM calls vs 2) | Planner uses cheapest model (~$0.001). Net cost may be lower if fewer tokens wasted |
| Complexity | Pipeline is 4 simple stages, each testable independently |
| Tool timeout kills valid long-running tools | 30s is generous; web_search typically completes in 2-5s |

---

## 8. Summary

| | Brain v1 | Brain v2 (Pipeline) |
|---|---------|---------------------|
| **Architecture** | 2-round sequential | Plan → Execute parallel → Synthesize |
| **Tool execution** | Sequential | Parallel with dependencies |
| **LLM calls** | 2 (fixed) | 2-3 (planner + synthesizer, optionally reflector) |
| **Latency** | High (sequential tools) | Lower (parallel tools) |
| **Cost** | ~$0.01-0.05/query | Similar or lower (cheaper planner model) |
| **Max tokens** | 2048 (fixed) | Dynamic per model |
| **Error handling** | Round 2 fallback | Per-tool timeout + graceful degradation |
| **Streaming** | Not used | Can stream synthesizer output |
| **Testing** | N/A | Feature flag, per-workspace, A/B ready |

---

## 9. Self-Improving Memory System

Brain v1 extracts facts passively. Brain v2 should **learn and improve** like a conversational AI agent with persistent memory. This is what makes the difference between a tool and a teammate.

### 9.1 Memory Types (Expanded)

v1 has: `fact`, `decision`, `commitment`, `policy`, `person`, `insight`

v2 adds:

| Type | Purpose | Example |
|------|---------|---------|
| **feedback** | User corrections — what NOT to do, what worked | "Don't summarize after every response" / "Single bundled PR was the right call" |
| **preference** | Per-member work style, communication preferences | "Nico prefers terse answers", "Alex likes bullet points" |
| **project** | Ongoing initiatives, deadlines, context not in code | "Merge freeze after March 5 for mobile release" |
| **reference** | Pointers to external systems | "Pipeline bugs tracked in Linear project INGEST" |
| **self** | Brain's own learned behaviors and patterns | "When asked about pricing, always check the PRICING.md first" |

### 9.2 Feedback Loop

When a user corrects Brain or confirms a non-obvious approach:

```
User: "no, don't create a separate channel for that — just post in #general"
  ↓
Reflector detects correction pattern ("no", "don't", negation + instruction)
  ↓
Saves feedback memory:
  type: feedback
  content: "Don't create new channels for one-off topics. Use #general."
  confidence: 0.9
  member_id: nico
  ↓
Next time similar request → feedback memory is in context → Brain behaves correctly
```

Also captures **positive confirmation** (harder to detect but equally important):
```
User: "perfect, exactly what I needed"
  ↓
Reflector: saves what approach was used and that it was validated
  type: feedback
  content: "Nico prefers detailed task breakdowns with due dates when planning sprints"
```

### 9.3 User Modeling

Per-member profile that accumulates over time:

```go
type MemberProfile struct {
    MemberID       string
    Role           string   // "data scientist", "frontend lead" — learned, not from DB
    Expertise      []string // ["Go", "infrastructure", "pricing"]
    Style          string   // "terse", "detailed", "visual"
    Preferences    []string // ["prefers bullet points", "hates emojis"]
    ActiveProjects []string // ["Brain 2.0", "mobile optimization"]
    LastUpdated    string
}
```

Built incrementally from:
- What they talk about (topic detection)
- How they respond to Brain (feedback signals)
- What they ask for (request patterns)
- What corrections they make (preference signals)

Injected into system prompt when that member talks to Brain:
```
You are talking to: **Nico** (Admin)
- Role: Full-stack developer, product lead
- Prefers: terse responses, no trailing summaries, structured plans before implementation
- Currently working on: Brain 2.0, mobile optimization
```

### 9.4 Behavioral Self-Improvement

The Reflector stage (runs async after every response) does:

1. **Response quality check** — Did the user follow up with a correction? Did they say thanks? Did they ignore the response?
2. **Pattern detection** — Is Brain making the same kind of error repeatedly?
3. **Self-memory creation** — Brain writes `self` type memories about its own behavior:
   - "When Nico asks about deployment, always mention both nexusteams.dev and nexus-workspace.fly.dev"
   - "When creating tasks, default to 'todo' status not 'backlog' — users kept changing it"
4. **SOUL evolution** — Periodically (weekly), Brain reviews its `self` memories and proposes updates to SOUL.md or INSTRUCTIONS.md (shown to admin for approval, not auto-applied)

### 9.5 Pinned Memories

Some memories should **always** be in context, not just when they match a semantic query:

```sql
ALTER TABLE brain_memories ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT FALSE;
```

Pinned memories are injected into the system prompt regardless of the user's message. Use cases:
- "Our pricing is $0 free, $29/mo pro" — always relevant when discussing the product
- "Never share API keys in chat" — safety constraint
- "Deploy process: make build → fly deploy" — operational knowledge

Admin can pin/unpin from the Brain memory panel.

### 9.6 Memory Lifecycle

```
Conversation message
  ↓
Rule-based extraction (instant, zero cost)
  ↓ every 30 messages
LLM extraction (batched, ~$0.01)
  ↓ after every Brain response
Reflector: feedback/preference/self detection (~$0.001)
  ↓ every 6 hours
Consolidation: dedup, supersede, create insights (~$0.01)
  ↓ weekly
Self-review: propose SOUL/INSTRUCTIONS updates (admin approval)
```

### 9.7 How This Changes the Pipeline

```
User message
  ↓
PLANNER (+ user profile + feedback memories + pinned memories)
  → Better plans because it knows user's preferences
  ↓
EXECUTOR (same as before)
  ↓
SYNTHESIZER (+ user profile → tailored response style)
  → Terse for Nico, detailed for Alex
  ↓
REFLECTOR (async)
  → Check for corrections/confirmations
  → Update user profile
  → Save feedback/self memories
  → No user-facing latency
```

---

## 10. Hermes Tool Calling — Research Findings

### 10.1 How Hermes Does It

Hermes models (NousResearch) are **fine-tuned on function-calling datasets** — tool use is in the model weights, not prompt-hacked. The format:

```
System prompt embeds tools in <tools> XML tags (OpenAI JSON schema inside)
  ↓
Model reasons in <scratch_pad> (GOAP: Goal → Actions → Observation → Reflection)
  ↓
Model outputs <tool_call>{"name": "...", "arguments": {...}}</tool_call>
  ↓
Runtime validates args against schema BEFORE execution
  ↓
Result fed back in <tool_response> with role: tool
  ↓
Model either calls another tool or produces final answer
  ↓
Loop continues up to max_depth (5-10 iterations)
```

### 10.2 What Hermes Does That We Don't

| Hermes Pattern | Nexus v1 | Impact |
|---|---|---|
| **Scratch pad planning** (GOAP reasoning before tool calls) | No planning — model picks tools blind | Wrong tool selection, unnecessary calls |
| **Argument validation** (type check, required args, enum check before execution) | No validation — raw JSON goes to executeTool() | Silent failures, bad data |
| **Self-correction loop** (error + traceback fed back, model retries) | 2 rounds max, error is a generic string | Can't recover from mistakes |
| **Configurable depth** (5-10 iterations) | Hardcoded 2 rounds | Complex tasks can't iterate |
| **Structured error feedback** (full schema + received args + traceback) | "Error: missing title" | Model can't understand what went wrong |
| **Tool scoping** ("one tool at a time" recommended) | All 20+ tools dumped every request | Model overwhelmed, picks wrong tools |

### 10.3 Ollama Translation Layer

Ollama handles format conversion automatically for Hermes models:

```
Nexus (OpenAI format) → Ollama API → Hermes XML tags → Model → XML response → Ollama → OpenAI format → Nexus
```

**No code changes needed for basic Hermes tool calling via Ollama.** The translation is built into Ollama's chat template system. Hermes GGUF files embed their own template.

### 10.4 Tools 2.0 — What Brain v2 Needs

#### A. Validation Layer

Before executing any tool call, validate against the schema:

```go
type ToolValidationError struct {
    Tool     string   `json:"tool"`
    Error    string   `json:"error"`
    Schema   any      `json:"schema"`   // expected
    Received any      `json:"received"` // what model sent
}

func validateToolCall(call ToolCall, schema ToolSchema) *ToolValidationError {
    // Check required arguments exist
    // Type-check each argument
    // Validate enum values
    // Return structured error if invalid
}
```

Invalid calls get fed back to the model with full context — not executed.

#### B. Self-Correction Loop

```go
maxDepth := getBrainSetting(slug, "tool_max_depth", "5")

for depth := 0; depth < maxDepth; depth++ {
    response, toolCalls := LLM.CompleteWithTools(...)

    if len(toolCalls) == 0 {
        break // Model is done, has final answer
    }

    for _, call := range toolCalls {
        if err := validateToolCall(call, schema); err != nil {
            // Feed structured error back, let model retry
            appendToolError(conversation, err)
            continue
        }
        result := executeWithTimeout(call, 30*time.Second)
        appendToolResult(conversation, result)
    }
}
```

#### C. Tool Scoping

The Planner stage selects relevant tools instead of dumping all 20+:

```go
// Planner output includes which tools are needed
type Plan struct {
    Steps []Step `json:"steps"`
    Tools []string `json:"tools"` // only these tools sent to executor
}

// Executor only passes selected tools to the LLM
scopedTools := filterTools(allTools, plan.Tools)
```

For simple queries ("what time is it?"), the model sees 2-3 tools instead of 25+.

#### D. Structured Error Feedback

When a tool fails (validation or execution), send rich context:

```json
{
    "tool": "create_task",
    "status": "error",
    "error": "missing required argument: title",
    "expected_schema": {
        "required": ["title"],
        "properties": {"title": {"type": "string"}, "status": {"type": "string", "enum": ["todo","in_progress","done"]}}
    },
    "received_arguments": {"status": "todo"},
    "hint": "Please provide the 'title' argument and retry."
}
```

The model gets enough context to self-correct on the next iteration.

#### E. Hermes-Compatible Local Mode

When running with Hermes via Ollama, Brain v2 can use Hermes-optimal settings:

| Setting | Cloud Models | Hermes Local |
|---------|-------------|--------------|
| Temperature | 0.7 | 0.8 (Hermes recommended) |
| Repetition penalty | 1.0 | 1.1 (Hermes recommended) |
| Tool scoping | Planner selects | "One tool at a time" (Hermes best practice) |
| Max depth | 5 | 5-10 (local = free, can iterate more) |
| Planning | LLM planner | Hermes scratch_pad (native, no extra call needed) |

When `ollama_model` is a Hermes variant, Brain v2 adjusts inference parameters automatically.

---

## 11. Updated Implementation Phases

| Phase | What | Details | Commit |
|-------|------|---------|--------|
| 1 | **Scaffold** | `brain2/` directory, feature flag (`brain_version` setting), route toggle (one `if/else` in ws.go), metrics struct | 1 |
| 2 | **Tool Validation** | `brain2/validate.go` — schema validation, structured errors, type checking | 1 |
| 3 | **Self-Correction Loop** | `brain2/executor.go` — configurable max_depth (default 5), error feedback, tool timeout (30s) | 1 |
| 4 | **Planner** | `brain2/planner.go` — fast model, tool scoping, plan JSON output, dependency graph | 1 |
| 5 | **Parallel Executor** | `brain2/executor.go` update — concurrent tool execution with dependency resolution | 1 |
| 6 | **Synthesizer** | `brain2/synthesizer.go` — dynamic MaxTokens per model, full context assembly (reuses v1 BuildSystemPrompt) | 1 |
| 7 | **Reflector** | `brain2/reflector.go` — async feedback detection, user profiles, self-memories, behavioral learning | 1 |
| 8 | **Memory 2.0** | Pinned memories, expanded types (feedback/preference/project/reference/self), member profiles | 1 |
| 9 | **Hermes Optimization** | Auto-detect Hermes models, adjust inference params, scratch_pad support, local-optimized settings | 1 |
| 10 | **UI** | Brain settings toggle (v1/v2 Beta), pinned memory management, tool depth setting | 1 |
| 11 | **A/B Evaluation** | Test protocol: v1 vs v2-cloud vs v2-hermes-local, same prompts, compare metrics | — |

### Phase Dependencies

```
Phase 1 (scaffold)
  ├── Phase 2 (validation) ── no dependencies, can start immediately
  ├── Phase 3 (self-correction) ── depends on 2 (uses validation)
  ├── Phase 4 (planner) ── independent
  └── Phase 8 (memory 2.0) ── independent

Phase 5 (parallel executor) ── depends on 3 + 4
Phase 6 (synthesizer) ── depends on 5
Phase 7 (reflector) ── depends on 6 + 8
Phase 9 (hermes) ── depends on 6
Phase 10 (UI) ── depends on all above
Phase 11 (evaluation) ── depends on 10
```

Phases 2, 4, and 8 can run in parallel after Phase 1.

---

## 12. Ollama Integration Status (Verified 2026-04-02)

### Current State: FUNCTIONAL

| Component | File | Status |
|-----------|------|--------|
| Direct client | `internal/brain/openrouter.go:247` | Complete — `NewOllamaClient()` |
| Bridge client | `internal/brain/bridge_client.go` | Complete — WebSocket proxy with tool support |
| Model routing | `internal/server/brain.go:588` | Complete — Ollama prioritized when enabled |
| Settings | brain_settings table | Complete — `ollama_enabled`, `ollama_model`, `ollama_url` |
| Frontend UI | +page.svelte | Complete — radio button, model input, URL input |
| Models API | `GET /workspaces/{slug}/ollama/models` | Complete — lists local models |
| Tool calling | Via Ollama's OpenAI-compat API | Works — Ollama translates to/from Hermes XML format |

### How to Test

```bash
ollama pull hermes3          # or llama3.2, mistral
ollama serve                 # starts on localhost:11434

# In Nexus UI → Brain → Settings:
# Engine: Ollama (Local AI)
# Model: hermes3
# URL: http://localhost:11434
# → Apply → @Brain in any channel
```

### Hermes-Specific Test

```bash
ollama pull nous-hermes2-pro    # Best for tool calling (7B)
ollama pull hermes3             # Latest, has scratch_pad planning
```

Both should work with Nexus tool calling through Ollama's translation layer. Hermes 3 will produce higher quality tool calls due to GOAP reasoning.

---

## 13. Risk Assessment (Updated)

| Risk | Mitigation |
|------|-----------|
| v2 breaks v1 | Separate code path, one `if/else` in ws.go, feature flag, instant rollback |
| Planner makes bad plans | Falls back to v1 behavior (direct tool calling, no plan) |
| Validation rejects valid calls | Validation is advisory in Phase 2 (log + execute), strict in Phase 3 |
| Hermes model not available | Falls back to cloud model via OpenRouter |
| Self-correction loops forever | Configurable max_depth (default 5), hard cap at 10 |
| Tool timeout kills long operations | 30s is generous; most tools complete in 1-5s |
| Memory 2.0 conflicts with v1 memories | Same table, new types additive, v1 ignores unknown types |
| Parallel tool execution race condition | Tools are stateless functions, no shared mutable state |

---

**Bottom line:** Brain v2 is not just a faster pipeline — it's a fundamentally better tool-calling system. Hermes-style validation, self-correction, and tool scoping address the root causes of v1's tool-calling failures. The self-improving memory system makes it learn from every interaction. And with Hermes models via Ollama, the entire system runs locally at zero cost. Test it in one workspace with a feature flag — zero risk to production.
