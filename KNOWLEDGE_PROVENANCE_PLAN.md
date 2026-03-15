# Knowledge Provenance Plan

## Overview

Make Brain's knowledge system traceable — not just "Brain knows X" but "Brain knows X because of Y, added by Z on date."

## Current State

### Memories (good provenance)
- `source` field: `rule`, `llm`, `pin`, `consolidation`
- `source_channel`: which channel the memory came from
- `source_message_id`: which message (rule + pin only, LLM extraction loses this)
- `participants`: who was involved
- `importance` + `confidence`: scored 0.0-1.0
- `type`: fact, decision, commitment, policy, person, insight

### Knowledge (weak provenance)
- `created_by`: user ID only
- `source_type`: text, pdf, file
- `source_name`: original filename (files only)
- **Missing**: source URL, no attribution in system prompt, no way for Brain to cite sources

## Changes

### 1. Add `source_url` to Knowledge

**Migration:**
```sql
ALTER TABLE brain_knowledge ADD COLUMN source_url TEXT DEFAULT '';
```

**Backend** (`internal/server/brain_knowledge.go`):
- In `handleImportKnowledgeURL()`: store the URL in `source_url` before saving
- In `handleCreateKnowledge()`: accept optional `source_url` field
- Return `source_url` in API responses

**Frontend:**
- Show source URL as clickable link in knowledge list
- Add optional "Source URL" field to manual knowledge creation form

### 2. Source Attribution in Context Injection

**File:** `internal/brain/knowledge.go` — `BuildKnowledgeContext()`

Change output format from:
```markdown
## Knowledge Base

### Marketing Strategy
Content here...
```

To:
```markdown
## Knowledge Base

### Marketing Strategy
_Source: strategy-deck.pdf, uploaded by Nico on Jan 15, 2026_

Content here...
```

This lets Brain naturally cite sources in responses: *"According to the strategy deck (uploaded Jan 15)..."*

**Implementation:**
- Query `created_by` (resolve to display name via member lookup)
- Include `source_name` or `source_url` if present
- Include `created_at` formatted as date

### 3. Memory Source Attribution in Context

**File:** `internal/brain/memory.go` — `BuildMemoryContext()`

Change output format from:
```markdown
## Decisions
- We decided to use Stripe for payments
```

To:
```markdown
## Decisions
- We decided to use Stripe for payments [Nico & Maria in #product, Jan 2]
```

Already have the data (`participants`, `source_channel`, `created_at`) — just need to format it.

### 4. LLM Memory Message Tracking

**Problem:** LLM extraction processes N messages at once, saves `source_channel` but not `source_message_id`.

**Fix** (`internal/server/brain_memory.go`):
- In `trackMessageAndMaybeExtract()`: pass the message IDs to the extraction prompt
- Ask LLM to return which message(s) each memory was derived from
- Store first/primary message ID in `source_message_id`

**LLM extraction prompt change:**
```
Each message has an ID prefix like [msg:abc123]. When extracting memories,
include the message ID that most directly supports each memory.
```

**Extraction response format:**
```json
{
  "type": "decision",
  "content": "We decided to use Stripe",
  "source_message": "msg_abc123",
  "confidence": 0.85
}
```

### 5. Confidence Reasoning

**Migration:**
```sql
ALTER TABLE brain_memories ADD COLUMN confidence_reason TEXT DEFAULT '';
```

**Backend** (`internal/server/brain_memory.go`):
- Add `confidence_reason` to extraction prompt: "Explain briefly why you scored this confidence level"
- LLM returns: `"confidence_reason": "Explicitly stated by two team members"`
- Store alongside confidence score

**Frontend** (`+page.svelte` memory list):
- Show confidence reason as tooltip or expandable detail on memory items

### 6. "Why do you know that?" Brain Skill

**New skill** (`internal/brain/skills/` or built-in tool):

When user asks "why do you think that?" or "where did you learn that?", Brain can:
1. Search memories + knowledge for terms from its last response
2. Return provenance chain:
   ```
   I know this because:
   - Memory: "We decided to use Stripe" (decision, confidence 0.85)
     Source: #product channel, message by Nico on Jan 2
   - Knowledge: "Payment Integration Guide"
     Source: payments.pdf, uploaded by Maria on Dec 15
   ```

**Implementation options:**
- A. Brain tool `trace_knowledge(query)` — searches memories + knowledge, returns sources
- B. Automatic — always append source references to Brain responses when citing knowledge
- C. On-demand skill — user triggers with "cite your sources" or similar

**Recommendation:** Option A (tool) — cleanest, doesn't bloat every response.

**Tool definition:**
```go
{
  Name: "trace_knowledge",
  Description: "Search for the source and provenance of something Brain knows. Use when asked 'why do you think that', 'where did you learn that', or 'cite your sources'.",
  Parameters: {
    "query": "The claim or fact to trace back to its source"
  }
}
```

**Tool handler:**
1. Search `brain_memories` by content similarity (existing FTS)
2. Search `brain_knowledge` by content similarity
3. For each match, return: content, source type, source channel/URL, created_by (resolved name), date, confidence + reason
4. Format as markdown list

## Verification

1. Upload a PDF to knowledge → verify `source_name` appears in Brain's context
2. Import URL → verify `source_url` is stored and displayed
3. Say something memorable in chat → wait for extraction → check memory has `source_message_id`
4. Ask Brain a question that uses knowledge → ask "where did you learn that?" → verify it cites the source
5. Check memory list in UI → confidence reasons visible

## Priority Order

1. Source attribution in context (items 2 + 3) — immediate improvement, ~1hr
2. Add `source_url` to knowledge (item 1) — ~30min
3. LLM memory message tracking (item 4) — ~2hrs
4. Confidence reasoning (item 5) — ~1hr
5. Trace knowledge tool (item 6) — ~2hrs
