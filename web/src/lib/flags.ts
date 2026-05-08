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
