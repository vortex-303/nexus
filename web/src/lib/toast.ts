import { writable } from 'svelte/store';

export type ToastType = 'success' | 'error' | 'info';

export interface Toast {
	id: number;
	message: string;
	type: ToastType;
	duration: number;
}

let nextId = 0;

export const toasts = writable<Toast[]>([]);

export function addToast(message: string, type: ToastType = 'info', duration = 5000) {
	const id = nextId++;
	toasts.update(t => [...t, { id, message, type, duration }]);
	setTimeout(() => {
		toasts.update(t => t.filter(toast => toast.id !== id));
	}, duration);
}
