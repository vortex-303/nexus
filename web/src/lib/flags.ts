// Feature flags for surfaces we want to hide without deleting their code.
//
// AGENT_CREATION_ENABLED — gates every UI affordance that lets a user
// create a new agent (user menu Agent Library, Team modal Create Agent
// button, org-chart "Create Agent for Role"). Existing agents continue
// to work normally — the backend createAgent / createAgentFromTemplate
// endpoints stay open, the @AgentName mention path is unchanged, and
// Brain's delegate_to_agent / ask_agent tools still call existing
// agents. Listing + editing existing agents stays accessible so admins
// can curate what's already there.
//
// Flip this back to true to restore creation paths in one edit.
export const AGENT_CREATION_ENABLED = false;

// BRAIN_V3_ENABLED — gates every UI affordance that lets a user pick
// the Claude Managed Agents engine (Brain v3) or configure it: the
// engine selector "Claude" option, Anthropic API key input, v3 system
// prompt template selector, v3 memory_store tab, v3 skills tab, and
// "Reset v3 agent" button. The backend code paths (internal/brain3/*,
// internal/server/brain3.go, brain3 routes, v3 migrations) stay live
// and continue to serve existing v3 workspaces — new workspaces just
// can't switch INTO v3 from the UI.
//
// Flip this back to true to restore the v3 surface in one edit.
// All v3 code is preserved.
export const BRAIN_V3_ENABLED = false;
