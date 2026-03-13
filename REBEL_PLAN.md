# Rebel Plan — Anti-SV Feature Roadmap

The thread connecting all of them: **show, don't tell.** Every SV company *says* they care about privacy. These features *prove* it structurally. You can't fake a network log. You can't fake a portable export. You can't fake showing the actual cost. The proof is the product.

---

## 1. Prompt Transparency Log

**Status:** Planned
**Name:** "Glass Box AI" / "AI Log"
**Effort:** Low-medium (data exists in `brain_action_log`, needs frontend surface)

Every AI interaction visible to the whole workspace. Every prompt Brain received, which model answered, what tools were called, how long it took, what it cost. Not just admins — everyone.

**Why:** The thing no competitor will ever copy. Slack/Copilot AI is a black box. Showing prompts reveals how much data they vacuum up. Making this visible is a structural advantage.

**Risk:** Low.
**Validation:** SOC 2, ISO 27001 teams. "Show me every AI interaction" — only tool that can answer.
**Landing page:** "Every AI prompt. Every model. Every response. Visible to your whole team. Try that with Copilot."
**Rebel points:** 9/10

---

## 2. One-File Workspace Export ← PRIORITY

**Status:** Next up
**Name:** "Portable Workspace"
**Effort:** Medium (SQLite is already storage, needs packaging + import endpoint)

Download entire workspace as a single `.nexus` file (SQLite + files). Import on any other Nexus instance. Your workspace is a file, not a subscription.

**Why:** Makes "you can leave anytime" concrete. Not a lossy CSV — the actual database with all relationships. Drop it on another server, everything works.

**Risk:** Low-medium. Care needed around user accounts and file references.
**Validation:** The moment someone downloads and runs offline = conversion event.
**Landing page:** "Your workspace is a file. Download it. Move it. Run it offline. Try asking Slack for that."
**Rebel points:** 10/10

---

## 3. Network Transparency Panel

**Status:** Planned
**Name:** "Connection Map" / "Network Log"
**Effort:** Low (log outbound HTTP calls, display destination + timestamp + purpose)

Settings page showing every external connection the workspace makes. Live proof that Nexus phones home to zero servers. Self-hosted shows only the model provider the user configured.

**Why:** Privacy policies are words. This is proof.
**Risk:** Low.
**Validation:** Security teams, CTOs burned by "privacy-first" tools with quiet telemetry. HN/Twitter screenshot bait.
**Landing page:** "See every connection your workspace makes. Spoiler: it's just the AI provider you chose."
**Rebel points:** 8/10

---

## 4. Self-Destruct ← PRIORITY

**Status:** Next up
**Name:** "Kill Switch"
**Effort:** Low (drop SQLite file + file directory, careful confirmation UX)

One button. Deletes everything. Not "request deletion" — instant, irreversible, verified. Cloud: wipes workspace DB, files, keys. Self-hosted: full removal. Optional hash proof of destruction.

**Why:** Every other platform makes leaving painful. GDPR requests take 30 days. Slack retains. Microsoft has "legal holds." Instant deletion is a statement: we don't want your data.

**Risk:** Medium (confirmation UX must prevent accidents).
**Validation:** Existence validates the privacy claim even if nobody uses it. Like a money-back guarantee.
**Landing page:** "One button. Everything gone. No 30-day wait. No retention policy. Gone."
**Rebel points:** 8/10

---

## 5. Visible System Prompts

**Status:** Planned
**Name:** "Open Prompts"
**Effort:** Very low (read-only view of existing definition files for non-admins)

Every Brain system prompt — SOUL.md, INSTRUCTIONS.md, North Star, memory context — viewable by any workspace member. "Here's exactly what your AI was told before it answered you."

**Why:** AI equivalent of open source. Every other AI product guards system prompts as trade secrets. Making them visible means the team understands *why* Brain answered that way.

**Risk:** Very low.
**Validation:** Developers and technical teams. Removes AI "magic," replaces with understanding.
**Landing page:** Medium impact. "Once you see it, you get it" feature.
**Rebel points:** 7/10

---

## 6. Cost Transparency

**Status:** Planned
**Name:** "AI Cost Meter"
**Effort:** Low (token counts + model pricing available, needs display)

Show actual cost of every AI interaction. "This response cost $0.003." Monthly workspace AI spend visible to everyone. Breakdown by model, user, channel.

**Why:** SV hides costs behind subscriptions so you don't realize you're overpaying. $30/month Copilot vs actual $2/month API cost. Showing real costs exposes the markup.

**Risk:** Low.
**Validation:** Finance teams, bootstrapped startups. "$14.20/month for the whole company" vs "$30/seat/month."
**Landing page:** "See what AI actually costs. Hint: it's not $30/user/month."
**Rebel points:** 9/10
