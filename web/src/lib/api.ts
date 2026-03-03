const BASE = '';

function getToken(): string | null {
	return localStorage.getItem('nexus_token');
}

export function setToken(token: string) {
	localStorage.setItem('nexus_token', token);
}

export function getWorkspaceSlug(): string | null {
	return localStorage.getItem('nexus_workspace');
}

export function setWorkspaceSlug(slug: string) {
	localStorage.setItem('nexus_workspace', slug);
}

export function clearSession() {
	localStorage.removeItem('nexus_token');
	localStorage.removeItem('nexus_workspace');
}

async function request(method: string, path: string, body?: any): Promise<any> {
	const headers: Record<string, string> = { 'Content-Type': 'application/json' };
	const token = getToken();
	if (token) headers['Authorization'] = `Bearer ${token}`;

	const res = await fetch(`${BASE}${path}`, {
		method,
		headers,
		body: body ? JSON.stringify(body) : undefined
	});

	const data = await res.json();
	if (!res.ok) throw new Error(data.error || 'Request failed');
	return data;
}

export async function createWorkspace(displayName: string, email?: string, password?: string) {
	const body: any = { display_name: displayName };
	if (email && password) { body.email = email; body.password = password; }
	const data = await request('POST', '/api/workspaces', body);
	setToken(data.token);
	setWorkspaceSlug(data.slug);
	return data;
}

export async function joinWorkspace(slug: string, displayName: string, inviteToken: string) {
	const data = await request('POST', `/api/workspaces/${slug}/join`, {
		display_name: displayName,
		invite_token: inviteToken
	});
	setToken(data.token);
	setWorkspaceSlug(slug);
	return data;
}

export async function getWorkspace(slug: string) {
	return request('GET', `/api/workspaces/${slug}`);
}

export async function createInvite(slug: string) {
	return request('POST', `/api/workspaces/${slug}/invite`);
}

export async function listChannels(slug: string) {
	return request('GET', `/api/workspaces/${slug}/channels`);
}

export async function createChannel(slug: string, name: string, type = 'public') {
	return request('POST', `/api/workspaces/${slug}/channels`, { name, type });
}

export async function getMessages(slug: string, channelId: string, before?: string) {
	let url = `/api/workspaces/${slug}/channels/${channelId}/messages`;
	if (before) url += `?before=${before}`;
	return request('GET', url);
}

export async function getOnlineMembers(slug: string) {
	return request('GET', `/api/workspaces/${slug}/online`);
}

export async function getMember(slug: string, memberId: string) {
	return request('GET', `/api/workspaces/${slug}/members/${memberId}`);
}

export async function updateMemberRole(slug: string, memberId: string, role: string) {
	return request('PUT', `/api/workspaces/${slug}/members/role`, { member_id: memberId, role });
}

export async function updateMemberPermission(slug: string, memberId: string, permission: string, granted: boolean | null) {
	return request('PUT', `/api/workspaces/${slug}/members/permission`, { member_id: memberId, permission, granted });
}

export async function kickMember(slug: string, memberId: string) {
	return request('DELETE', `/api/workspaces/${slug}/members/${memberId}`);
}

export async function listRoles() {
	return request('GET', '/api/roles');
}

// Tasks
export async function listTasks(slug: string, filters?: { status?: string; assignee?: string; priority?: string }) {
	let url = `/api/workspaces/${slug}/tasks`;
	const params = new URLSearchParams();
	if (filters?.status) params.set('status', filters.status);
	if (filters?.assignee) params.set('assignee', filters.assignee);
	if (filters?.priority) params.set('priority', filters.priority);
	const qs = params.toString();
	if (qs) url += `?${qs}`;
	return request('GET', url);
}

export async function createTask(slug: string, task: {
	title: string; description?: string; status?: string; priority?: string;
	assignee_id?: string; due_date?: string; tags?: string[];
}) {
	return request('POST', `/api/workspaces/${slug}/tasks`, task);
}

export async function updateTask(slug: string, taskId: string, updates: Record<string, any>) {
	return request('PUT', `/api/workspaces/${slug}/tasks/${taskId}`, updates);
}

export async function deleteTask(slug: string, taskId: string) {
	return request('DELETE', `/api/workspaces/${slug}/tasks/${taskId}`);
}

// Documents
export async function listDocs(slug: string) {
	return request('GET', `/api/workspaces/${slug}/documents`);
}

export async function createDoc(slug: string, doc: { title?: string; content?: string }) {
	return request('POST', `/api/workspaces/${slug}/documents`, doc);
}

export async function updateDoc(slug: string, docId: string, updates: { title?: string; content?: string }) {
	return request('PUT', `/api/workspaces/${slug}/documents/${docId}`, updates);
}

export async function deleteDoc(slug: string, docId: string) {
	return request('DELETE', `/api/workspaces/${slug}/documents/${docId}`);
}

// Files
export async function uploadFile(slug: string, channelId: string, file: File): Promise<any> {
	const formData = new FormData();
	formData.append('file', file);
	const token = getToken();
	const res = await fetch(`/api/workspaces/${slug}/channels/${channelId}/files`, {
		method: 'POST',
		headers: token ? { Authorization: `Bearer ${token}` } : {},
		body: formData,
	});
	const data = await res.json();
	if (!res.ok) throw new Error(data.error || 'Upload failed');
	return data;
}

export async function listFiles(slug: string, channelId?: string) {
	let url = `/api/workspaces/${slug}/files`;
	if (channelId) url += `?channel_id=${channelId}`;
	return request('GET', url);
}

export function fileUrl(slug: string, hash: string): string {
	return `/api/workspaces/${slug}/files/${hash}`;
}

// Brain
export async function getBrainSettings(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/settings`);
}

export async function updateBrainSettings(slug: string, settings: Record<string, string>) {
	return request('PUT', `/api/workspaces/${slug}/brain/settings`, settings);
}

export async function getBrainDefinition(slug: string, file: string) {
	return request('GET', `/api/workspaces/${slug}/brain/definitions/${file}`);
}

export async function updateBrainDefinition(slug: string, file: string, content: string) {
	return request('PUT', `/api/workspaces/${slug}/brain/definitions/${file}`, { content });
}

// Brain Memory
export async function listMemories(slug: string, type?: string) {
	let url = `/api/workspaces/${slug}/brain/memories`;
	if (type) url += `?type=${type}`;
	return request('GET', url);
}

export async function deleteMemory(slug: string, memoryId: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/memories/${memoryId}`);
}

export async function clearMemories(slug: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/memories`);
}

// Brain Actions (Observability)
export async function listActions(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/actions`);
}

// Brain Skills
export async function listSkills(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/skills`);
}

export async function getSkill(slug: string, file: string) {
	return request('GET', `/api/workspaces/${slug}/brain/skills/${file}`);
}

export async function updateSkill(slug: string, file: string, content: string) {
	return request('PUT', `/api/workspaces/${slug}/brain/skills/${file}`, { content });
}

export async function deleteSkill(slug: string, file: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/skills/${file}`);
}

export function getCurrentUser(): { uid: string; name: string; ws: string; role: string; aid?: string; email?: string; sa?: boolean } | null {
	const token = getToken();
	if (!token) return null;
	try {
		const payload = JSON.parse(atob(token.split('.')[1]));
		return { uid: payload.uid, name: payload.name, ws: payload.ws, role: payload.role, aid: payload.aid, email: payload.email, sa: payload.sa };
	} catch {
		return null;
	}
}

export function getWSUrl(): string {
	const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
	return `${proto}//${location.host}/ws?token=${getToken()}`;
}

// Auth
export async function register(email: string, password: string, displayName: string) {
	return request('POST', '/api/auth/register', { email, password, display_name: displayName });
}

export async function login(email: string, password: string, workspaceSlug?: string) {
	const data = await request('POST', '/api/auth/login', { email, password, workspace_slug: workspaceSlug || '' });
	setToken(data.token);
	if (workspaceSlug) setWorkspaceSlug(workspaceSlug);
	return data;
}

export async function listWorkspaces() {
	return request('GET', '/api/auth/workspaces');
}

// Brain Knowledge
export async function listKnowledge(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/knowledge`);
}

export async function createKnowledge(slug: string, article: { title: string; content: string }) {
	return request('POST', `/api/workspaces/${slug}/brain/knowledge`, article);
}

export async function uploadKnowledge(slug: string, file: File): Promise<any> {
	const formData = new FormData();
	formData.append('file', file);
	const token = getToken();
	const res = await fetch(`/api/workspaces/${slug}/brain/knowledge/upload`, {
		method: 'POST',
		headers: token ? { Authorization: `Bearer ${token}` } : {},
		body: formData,
	});
	const data = await res.json();
	if (!res.ok) throw new Error(data.error || 'Upload failed');
	return data;
}

export async function updateKnowledge(slug: string, id: string, article: { title: string; content: string }) {
	return request('PUT', `/api/workspaces/${slug}/brain/knowledge/${id}`, article);
}

export async function deleteKnowledge(slug: string, id: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/knowledge/${id}`);
}

// Knowledge URL Import
export async function importKnowledgeURL(slug: string, url: string) {
	return request('POST', `/api/workspaces/${slug}/brain/knowledge/import-url`, { url });
}

// User Preferences
export async function getMe() {
	return request('GET', '/api/auth/me');
}

export async function updateMe(data: { display_name?: string; email?: string }) {
	return request('PUT', '/api/auth/me', data);
}

export async function changePassword(data: { current_password: string; new_password: string }) {
	return request('PUT', '/api/auth/me/password', data);
}

// Agents
export async function listAgents(slug: string) {
	return request('GET', `/api/workspaces/${slug}/agents`);
}

export async function getAgent(slug: string, agentId: string) {
	return request('GET', `/api/workspaces/${slug}/agents/${agentId}`);
}

export async function createAgent(slug: string, agent: any) {
	return request('POST', `/api/workspaces/${slug}/agents`, agent);
}

export async function updateAgent(slug: string, agentId: string, updates: any) {
	return request('PUT', `/api/workspaces/${slug}/agents/${agentId}`, updates);
}

export async function deleteAgent(slug: string, agentId: string) {
	return request('DELETE', `/api/workspaces/${slug}/agents/${agentId}`);
}

export async function listAgentTemplates(slug: string) {
	return request('GET', `/api/workspaces/${slug}/agents/templates`);
}

export async function createAgentFromTemplate(slug: string, templateId: string) {
	return request('POST', `/api/workspaces/${slug}/agents/from-template`, { template_id: templateId });
}

export async function generateAgentConfig(slug: string, description: string) {
	return request('POST', `/api/workspaces/${slug}/agents/generate`, { description });
}

// Org Chart
export async function getOrgChart(slug: string) {
	return request('GET', `/api/workspaces/${slug}/org-chart`);
}

export async function updateOrgPosition(slug: string, nodeId: string, reportsTo: string) {
	return request('PUT', `/api/workspaces/${slug}/org-chart/position`, { node_id: nodeId, reports_to: reportsTo });
}

export async function updateMemberProfile(slug: string, memberId: string, profile: { title?: string; bio?: string; goals?: string }) {
	return request('PUT', `/api/workspaces/${slug}/members/${memberId}/profile`, profile);
}

// Org Roles
export async function createOrgRole(slug: string, role: { title: string; description?: string; reports_to?: string }) {
	return request('POST', `/api/workspaces/${slug}/org-chart/roles`, role);
}

export async function updateOrgRole(slug: string, roleId: string, updates: { title?: string; description?: string; reports_to?: string }) {
	return request('PUT', `/api/workspaces/${slug}/org-chart/roles/${roleId}`, updates);
}

export async function deleteOrgRole(slug: string, roleId: string) {
	return request('DELETE', `/api/workspaces/${slug}/org-chart/roles/${roleId}`);
}

export async function fillOrgRole(slug: string, roleId: string, filledBy: string, filledType: string) {
	return request('PUT', `/api/workspaces/${slug}/org-chart/roles/${roleId}/fill`, { filled_by: filledBy, filled_type: filledType });
}

// Agent Skills
export async function listAgentSkills(slug: string, agentId: string) {
	return request('GET', `/api/workspaces/${slug}/agents/${agentId}/skills`);
}

export async function getAgentSkill(slug: string, agentId: string, file: string) {
	return request('GET', `/api/workspaces/${slug}/agents/${agentId}/skills/${file}`);
}

export async function updateAgentSkill(slug: string, agentId: string, file: string, content: string) {
	return request('PUT', `/api/workspaces/${slug}/agents/${agentId}/skills/${file}`, { content });
}

export async function deleteAgentSkill(slug: string, agentId: string, file: string) {
	return request('DELETE', `/api/workspaces/${slug}/agents/${agentId}/skills/${file}`);
}

// Models
export async function browseModels() {
	return request('GET', '/api/models/browse');
}

export async function getPinnedModels() {
	return request('GET', '/api/models');
}

// Announcements
export async function getAnnouncement() {
	return request('GET', '/api/announcements');
}

// Platform Admin
export async function adminStats() {
	return request('GET', '/api/admin/stats');
}

export async function adminListWorkspaces() {
	return request('GET', '/api/admin/workspaces');
}

export async function adminListAccounts() {
	return request('GET', '/api/admin/accounts');
}

export async function adminSuspendWorkspace(slug: string, suspended: boolean, reason = '') {
	return request('PUT', `/api/admin/workspaces/${slug}/suspend`, { suspended, reason });
}

export async function adminDeleteWorkspace(slug: string) {
	return request('DELETE', `/api/admin/workspaces/${slug}`);
}

export async function adminBanAccount(accountId: string, banned: boolean) {
	return request('PUT', `/api/admin/accounts/${accountId}/ban`, { banned });
}

export async function adminImpersonate(workspaceSlug: string, memberId: string) {
	return request('POST', '/api/admin/impersonate', { workspace_slug: workspaceSlug, member_id: memberId });
}

export async function adminEnterWorkspace(slug: string) {
	return request('POST', `/api/admin/workspaces/${slug}/enter`);
}

export async function adminAuditLog() {
	return request('GET', '/api/admin/audit');
}

export async function adminWorkspaceDetail(slug: string) {
	return request('GET', `/api/admin/workspaces/${slug}/detail`);
}

export async function adminExportWorkspace(slug: string): Promise<Blob> {
	const token = localStorage.getItem('nexus_token');
	const res = await fetch(`/api/admin/workspaces/${slug}/export`, {
		headers: token ? { Authorization: `Bearer ${token}` } : {},
	});
	if (!res.ok) throw new Error('Export failed');
	return res.blob();
}

export async function adminResetPassword(accountId: string, newPassword: string) {
	return request('PUT', `/api/admin/accounts/${accountId}/password`, { new_password: newPassword });
}

export async function adminSetAnnouncement(message: string, type: string = 'info') {
	return request('POST', '/api/admin/announcements', { message, type });
}

export async function adminClearAnnouncement() {
	return request('DELETE', '/api/admin/announcements');
}

export async function adminGetModels() {
	return request('GET', '/api/admin/models');
}

export async function adminSetModels(models: any[]) {
	return request('PUT', '/api/admin/models', { models });
}

// Webhooks
export async function createWebhook(slug: string, channelId: string, description: string) {
	return request('POST', `/api/workspaces/${slug}/brain/webhooks`, { channel_id: channelId, description });
}

export async function listWebhooks(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/webhooks`);
}

export async function deleteWebhook(slug: string, id: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/webhooks/${id}`);
}

export async function listWebhookEvents(slug: string, hookId: string) {
	return request('GET', `/api/workspaces/${slug}/brain/webhooks/${hookId}/events`);
}

// Email Threads
export async function listEmailThreads(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/email/threads`);
}

export async function deleteEmailThread(slug: string, id: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/email/threads/${id}`);
}

// Telegram Chats
export async function listTelegramChats(slug: string) {
	return request('GET', `/api/workspaces/${slug}/brain/telegram/chats`);
}

export async function deleteTelegramChat(slug: string, id: string) {
	return request('DELETE', `/api/workspaces/${slug}/brain/telegram/chats/${id}`);
}
