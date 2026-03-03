import { getWSUrl } from './api';

export type WSHandler = (type: string, payload: any) => void;

let socket: WebSocket | null = null;
let handlers: WSHandler[] = [];
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

export function connect() {
	if (socket?.readyState === WebSocket.OPEN) return;

	const url = getWSUrl();
	socket = new WebSocket(url);

	socket.onopen = () => {
		console.log('[ws] connected');
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
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
		reconnectTimer = setTimeout(connect, 2000);
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
	socket?.close();
	socket = null;
	handlers = [];
}

export function send(type: string, payload: any) {
	if (socket?.readyState !== WebSocket.OPEN) return;
	socket.send(JSON.stringify({ type, payload }));
}

export function onMessage(handler: WSHandler) {
	handlers = [handler]; // Only one handler at a time
	return () => {
		handlers = handlers.filter((h) => h !== handler);
	};
}

export function sendMessage(channelId: string, content: string) {
	send('message.send', { channel_id: channelId, content });
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
