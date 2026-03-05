package roles

// Role identifies a member's role in a workspace.
type Role string

const (
	RoleAdmin                Role = "admin"
	RoleMember               Role = "member"
	RoleDesigner             Role = "designer"
	RoleMarketingCoordinator Role = "marketing_coordinator"
	RoleMarketingStrategist  Role = "marketing_strategist"
	RoleResearcher           Role = "researcher"
	RoleSales                Role = "sales"
	RoleGuest                Role = "guest"
	RoleCustom               Role = "custom"
)

var ValidRoles = map[Role]bool{
	RoleAdmin: true, RoleMember: true, RoleDesigner: true,
	RoleMarketingCoordinator: true, RoleMarketingStrategist: true,
	RoleResearcher: true, RoleSales: true, RoleGuest: true, RoleCustom: true,
}

func IsValid(r string) bool {
	return ValidRoles[Role(r)]
}

// Permission is an action a member can perform.
type Permission string

const (
	// Chat
	PermChatSend       Permission = "chat.send"
	PermChatEdit       Permission = "chat.edit_own"
	PermChatDelete     Permission = "chat.delete_own"
	PermChatDeleteAny  Permission = "chat.delete_any"
	PermChatReact      Permission = "chat.react"
	PermChannelCreate  Permission = "channel.create"
	PermChannelArchive Permission = "channel.archive"

	// Tasks
	PermTaskCreate Permission = "task.create"
	PermTaskAssign Permission = "task.assign"
	PermTaskEdit   Permission = "task.edit"
	PermTaskDelete Permission = "task.delete"

	// Contacts / CRM
	PermContactView   Permission = "contact.view"
	PermContactCreate Permission = "contact.create"
	PermContactEdit   Permission = "contact.edit"
	PermContactDelete Permission = "contact.delete"

	// Brain
	PermBrainMention Permission = "brain.mention"
	PermBrainDM      Permission = "brain.dm"
	PermBrainConfig  Permission = "brain.config"

	// Agents
	PermAgentCreate Permission = "agent.create"
	PermAgentManage Permission = "agent.manage"

	// Documents
	PermDocCreate Permission = "doc.create"
	PermDocEdit   Permission = "doc.edit"
	PermDocDelete Permission = "doc.delete"

	// Files
	PermFileUpload Permission = "file.upload"

	// Knowledge
	PermKnowledgeManage Permission = "knowledge.manage"

	// Calendar
	PermEventCreate Permission = "event.create"
	PermEventEdit   Permission = "event.edit"
	PermEventDelete Permission = "event.delete"

	// Skills
	PermSkillManage Permission = "skill.manage"

	// Workspace
	PermWorkspaceSettings Permission = "workspace.settings"
	PermWorkspaceInvite   Permission = "workspace.invite"
	PermWorkspaceRoles    Permission = "workspace.roles"
	PermWorkspaceKick     Permission = "workspace.kick"
)

// AllPermissions is the complete set.
var AllPermissions = []Permission{
	PermChatSend, PermChatEdit, PermChatDelete, PermChatDeleteAny, PermChatReact,
	PermChannelCreate, PermChannelArchive,
	PermTaskCreate, PermTaskAssign, PermTaskEdit, PermTaskDelete,
	PermEventCreate, PermEventEdit, PermEventDelete,
	PermContactView, PermContactCreate, PermContactEdit, PermContactDelete,
	PermBrainMention, PermBrainDM, PermBrainConfig,
	PermAgentCreate, PermAgentManage,
	PermDocCreate, PermDocEdit, PermDocDelete,
	PermFileUpload,
	PermKnowledgeManage,
	PermSkillManage,
	PermWorkspaceSettings, PermWorkspaceInvite, PermWorkspaceRoles, PermWorkspaceKick,
}

// DefaultPermissions maps each role to its default permission set.
var DefaultPermissions = map[Role]map[Permission]bool{
	RoleAdmin: allPermsSet(),

	RoleMember: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermEventCreate, PermEventEdit, PermEventDelete,
		PermContactView,
		PermBrainMention, PermBrainDM,
		PermAgentCreate, PermDocCreate, PermDocEdit, PermFileUpload,
		PermSkillManage,
		PermWorkspaceInvite,
	),

	RoleDesigner: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermEventCreate, PermEventEdit,
		PermContactView,
		PermBrainMention, PermBrainDM,
		PermAgentCreate, PermDocCreate, PermDocEdit, PermFileUpload,
		PermWorkspaceInvite,
	),

	RoleMarketingCoordinator: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermEventCreate, PermEventEdit, PermEventDelete,
		PermContactView, PermContactCreate, PermContactEdit,
		PermBrainMention, PermBrainDM,
		PermAgentCreate, PermDocCreate, PermDocEdit, PermFileUpload,
		PermWorkspaceInvite,
	),

	RoleMarketingStrategist: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermEventCreate, PermEventEdit, PermEventDelete,
		PermContactView, PermContactCreate, PermContactEdit,
		PermBrainMention, PermBrainDM,
		PermAgentCreate, PermDocCreate, PermDocEdit, PermFileUpload,
		PermWorkspaceInvite,
	),

	RoleResearcher: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskEdit,
		PermEventCreate, PermEventEdit,
		PermContactView,
		PermBrainMention, PermBrainDM,
		PermDocCreate, PermDocEdit, PermFileUpload,
	),

	RoleSales: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermEventCreate, PermEventEdit, PermEventDelete,
		PermContactView, PermContactCreate, PermContactEdit, PermContactDelete,
		PermBrainMention, PermBrainDM,
		PermAgentCreate, PermDocCreate, PermDocEdit, PermFileUpload,
		PermWorkspaceInvite,
	),

	RoleGuest: permSet(
		PermChatSend, PermChatEdit, PermChatReact,
		PermContactView,
	),

	RoleCustom: permSet(
		PermChatSend, PermChatEdit, PermChatReact,
		PermDocCreate,
	),
}

func allPermsSet() map[Permission]bool {
	s := make(map[Permission]bool, len(AllPermissions))
	for _, p := range AllPermissions {
		s[p] = true
	}
	return s
}

func permSet(perms ...Permission) map[Permission]bool {
	s := make(map[Permission]bool, len(perms))
	for _, p := range perms {
		s[p] = true
	}
	return s
}
