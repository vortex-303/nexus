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
	PermContactView, PermContactCreate, PermContactEdit, PermContactDelete,
	PermBrainMention, PermBrainDM, PermBrainConfig,
	PermWorkspaceSettings, PermWorkspaceInvite, PermWorkspaceRoles, PermWorkspaceKick,
}

// DefaultPermissions maps each role to its default permission set.
var DefaultPermissions = map[Role]map[Permission]bool{
	RoleAdmin: allPermsSet(),

	RoleMember: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermContactView,
		PermBrainMention, PermBrainDM,
		PermWorkspaceInvite,
	),

	RoleDesigner: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermContactView,
		PermBrainMention, PermBrainDM,
		PermWorkspaceInvite,
	),

	RoleMarketingCoordinator: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermContactView, PermContactCreate, PermContactEdit,
		PermBrainMention, PermBrainDM,
		PermWorkspaceInvite,
	),

	RoleMarketingStrategist: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermContactView, PermContactCreate, PermContactEdit,
		PermBrainMention, PermBrainDM,
		PermWorkspaceInvite,
	),

	RoleResearcher: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskEdit,
		PermContactView,
		PermBrainMention, PermBrainDM,
	),

	RoleSales: permSet(
		PermChatSend, PermChatEdit, PermChatDelete, PermChatReact,
		PermChannelCreate,
		PermTaskCreate, PermTaskAssign, PermTaskEdit,
		PermContactView, PermContactCreate, PermContactEdit, PermContactDelete,
		PermBrainMention, PermBrainDM,
		PermWorkspaceInvite,
	),

	RoleGuest: permSet(
		PermChatSend, PermChatEdit, PermChatReact,
		PermContactView,
	),

	RoleCustom: permSet(
		PermChatSend, PermChatEdit, PermChatReact,
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
