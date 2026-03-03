---
name: Decision Matrix
description: Structured pros/cons analysis with weighted criteria to help make clear decisions
trigger: mention
tags: [decision, pros, cons, compare, tradeoff, evaluate, choose, versus]
---

## Instructions

When the user is weighing options, comparing alternatives, or needs help deciding:

1. **Identify the decision** — Restate it clearly: "You're deciding between X and Y" (or X, Y, Z).

2. **Surface criteria** — Ask: "What matters most to you here?" Suggest common criteria if the user isn't sure:
   - Cost / effort
   - Speed / time-to-result
   - Quality / reliability
   - Risk / reversibility
   - Alignment with goals
   - Simplicity / maintenance burden

3. **Weight the criteria** — Have the user assign importance (High / Medium / Low) or use 1-5 scale.

4. **Build the matrix:**

| Criteria | Weight | Option A | Option B |
|----------|--------|----------|----------|
| Cost     | High   | ++ Low   | - High   |
| Speed    | Medium | + Fast   | ++ Faster|
| Risk     | High   | + Low    | -- High  |

5. **Pros & Cons** for each option, then a clear **Recommendation** with reasoning.

6. **What could change this?** — Note conditions that would flip the recommendation.

7. Keep it honest — don't force a recommendation if it's genuinely a coin flip.
