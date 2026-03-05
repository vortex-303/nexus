import { writable } from 'svelte/store';
import { getWSUrl } from './api';

export type WSHandler = (type: string, payload: any) => void;

export const connectionStatus = writable<'connected' | 'connecting' | 'disconnected'>('disconnected');

let socket: WebSocket | null = null;
let handlers: WSHandler[] = [];
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempts = 0;

interface QueuedMessage {
	type: string;
	payload: any;
}
let offlineQueue: QueuedMessage[] = [];

// Ephemeral event types that should not be queued
const EPHEMERAL_TYPES = new Set(['typing.start', 'typing.stop', 'channel.read']);

function getReconnectDelay(): number {
	const base = 1000;
	const cap = 30000;
	const delay = Math.min(base * Math.pow(2, reconnectAttempts), cap);
	// Add jitter: +/- 1s
	return delay + (Math.random() * 2000 - 1000);
}

export function connect() {
	if (socket?.readyState === WebSocket.OPEN) return;

	connectionStatus.set('connecting');
	const url = getWSUrl();
	socket = new WebSocket(url);

	socket.onopen = () => {
		console.log('[ws] connected');
		connectionStatus.set('connected');
		const wasReconnect = reconnectAttempts > 0;
		reconnectAttempts = 0;
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}

		// Drain offline queue
		const queue = offlineQueue;
		offlineQueue = [];
		for (const msg of queue) {
			send(msg.type, msg.payload);
		}

		// Notify handlers of reconnection
		if (wasReconnect) {
			for (const h of handlers) {
				h('_reconnected', {});
			}
		}
	};

	socket.onmessage = (event) => {
		try {
			const envelope = JSON.parse(event.data);
			const payload = typeof envelope.payload === 'string'
				? JSON.parse(envelope.payload)
				: envelope.payload;
			for (const h of handlers) {
				h(envelope.type, payload);
			}
		} catch (e) {
			console.error('[ws] parse error:', e);
		}
	};

	socket.onclose = () => {
		console.log('[ws] disconnected');
		socket = null;
		connectionStatus.set('disconnected');
		reconnectAttempts++;
		const delay = getReconnectDelay();
		console.log(`[ws] reconnecting in ${Math.round(delay)}ms (attempt ${reconnectAttempts})`);
		reconnectTimer = setTimeout(connect, delay);
	};

	socket.onerror = () => {
		socket?.close();
	};
}

export function disconnect() {
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	reconnectAttempts = 0;
	offlineQueue = [];
	socket?.close();
	socket = null;
	handlers = [];
	connectionStatus.set('disconnected');
}

export function send(type: string, payload: any): boolean {
	if (socket?.readyState !== WebSocket.OPEN) {
		// Queue non-ephemeral messages for later
		if (!EPHEMERAL_TYPES.has(type)) {
			offlineQueue.push({ type, payload });
		}
		return false;
	}
	socket.send(JSON.stringify({ type, payload }));
	return true;
}

export function onMessage(handler: WSHandler) {
	handlers.push(handler);
	return () => {
		handlers = handlers.filter((h) => h !== handler);
	};
}

let clientIdCounter = 0;
export function generateClientId(): string {
	return `${Date.now()}-${++clientIdCounter}`;
}

export function sendMessage(channelId: string, content: string, clientId?: string, parentId?: string): boolean {
	const payload: any = { channel_id: channelId, content, client_id: clientId };
	if (parentId) payload.parent_id = parentId;
	return send('message.send', payload);
}

export function sendTyping(channelId: string) {
	send('typing.start', { channel_id: channelId });
}

export function sendReaction(messageId: string, channelId: string, emoji: string) {
	send('reaction.add', { message_id: messageId, channel_id: channelId, emoji });
}

export function removeReaction(messageId: string, channelId: string, emoji: string) {
	send('reaction.remove', { message_id: messageId, channel_id: channelId, emoji });
}

export function clearChannel(channelId: string) {
	send('channel.clear', { channel_id: channelId });
}

export function markChannelRead(channelId: string) {
	send('channel.read', { channel_id: channelId });
}
