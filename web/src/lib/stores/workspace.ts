import { writable } from 'svelte/store';

export interface Channel {
	id: string;
	name: string;
	type: string;
	classification: string;
	unread?: number;
	is_favorite?: boolean;
}

export interface Member {
	id: string;
	display_name: string;
	role: string;
	online?: boolean;
}

export interface Message {
	id: string;
	channel_id: string;
	sender_id: string;
	sender_name: string;
	content: string;
	created_at: string;
	edited_at?: string;
	reactions?: { emoji: string; count: number; users: string[] }[];
	status?: 'pending' | 'sent' | 'failed';
	clientId?: string;
	parent_id?: string;
	reply_count?: number;
	latest_reply_at?: string;
}

export const channels = writable<Channel[]>([]);
export const members = writable<Member[]>([]);
export const messages = writable<Message[]>([]);
export const activeChannel = writable<Channel | null>(null);
export const typingUsers = writable<Map<string, string>>(new Map());
export const onlineUsers = writable<Set<string>>(new Set());
