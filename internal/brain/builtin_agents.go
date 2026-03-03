package brain

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed skills/shared/*.md
var SharedSkillsFS embed.FS

//go:embed skills/creative_director/*.md
var CreativeDirectorSkillsFS embed.FS

// BuiltinAgent defines a system agent that ships with every workspace.
type BuiltinAgent struct {
	ID              string
	MemberID        string // matches member row ID
	Name            string
	Avatar          string
	Role            string
	Goal            string
	Backstory       string
	Instructions    string
	Constraints     string
	Model           string   // model override (empty = use workspace default)
	Tools           []string
	SkillsFS        embed.FS // embedded skill files
	SkillsDir       string   // subdirectory within the embed.FS
	KnowledgeAccess bool
	MemoryAccess    bool
}

// BuiltinAgents is the registry of all built-in agents (excluding Brain, which is special).
var BuiltinAgents = []BuiltinAgent{
	{
		ID:       "creative_director",
		MemberID: "creative_director",
		Name:     "Creative Director",
		Avatar:   "\U0001f3a8", // 🎨
		Role:     "Creative Director",
		Goal:     "Lead creative strategy, generate campaign concepts, produce ad visuals, and maintain brand consistency across all creative outputs.",
		Backstory: "You are an experienced Creative Director with a sharp eye for design, compelling copy, and brand storytelling. " +
			"You've led campaigns for startups and Fortune 500s alike. You think visually and can generate images to bring concepts to life.",
		Instructions: `You are the Creative Director for this workspace. Your primary workflows:

1. **Campaign Ideation**: When given a brief, generate 3-5 campaign concepts with themes, taglines, and visual direction.
2. **Ad Creative**: Analyze briefs, craft optimal image prompts, and generate visuals. Present image + copy together.
3. **Brand Consistency**: Maintain brand guidelines across all outputs. Flag inconsistencies.
4. **Creative Review**: Provide structured critique of creative assets with actionable feedback.

## Image Generation
You can generate images directly in your responses. When creating visuals:
- Describe what you want to create clearly
- Consider composition, color, mood, and typography space
- Generate the image as part of your response
- Iterate based on feedback

## Skill Usage
When using a specific skill, prefix your response with the skill tag (e.g., [skill:Campaign Ideation]).
This helps the team understand which workflow you're following.

## Communication Style
- Think visually — describe what things look like
- Be opinionated about creative direction
- Present options, not just one answer
- Use industry terminology naturally
- Keep copy tight and impactful`,
		Constraints: "Stay within the creative domain. For technical, financial, or operational questions, suggest involving the appropriate team member.",
		Model:       "google/gemini-2.5-flash",
		Tools:       []string{"create_task", "search_messages", "create_document", "generate_image"},
		SkillsFS:    CreativeDirectorSkillsFS,
		SkillsDir:   "skills/creative_director",
		KnowledgeAccess: true,
		MemoryAccess:    true,
	},
}

// SeedAgentSkills writes embedded skill .md files to an agent's skills directory on disk.
// Only writes files that don't already exist (preserves user edits).
func SeedAgentSkills(dataDir, slug, agentID string, skillsFS embed.FS, skillsDir string) error {
	destDir := AgentSkillsDir(dataDir, slug, agentID)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	entries, err := fs.ReadDir(skillsFS, skillsDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}

		destPath := filepath.Join(destDir, entry.Name())
		// Only write if file doesn't exist (preserve user edits)
		if _, err := os.Stat(destPath); err == nil {
			continue
		}

		data, err := skillsFS.ReadFile(filepath.Join(skillsDir, entry.Name()))
		if err != nil {
			continue
		}

		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return err
		}
	}

	return nil
}

// SeedSharedSkills writes shared skill .md files to an agent's skills directory.
func SeedSharedSkills(dataDir, slug, agentID string) error {
	return SeedAgentSkills(dataDir, slug, agentID, SharedSkillsFS, "skills/shared")
}
