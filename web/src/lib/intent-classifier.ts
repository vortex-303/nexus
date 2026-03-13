/**
 * Regex-based intent classifier for WebLLM pre-fetch strategy.
 * Runs client-side in <1ms to determine what data the server should fetch
 * before a single LLM inference call.
 */

export type Intent = 'web_search' | 'tasks' | 'calendar' | 'documents' | 'messages' | 'general';

const patterns: [Intent, RegExp][] = [
	['web_search', /\b(search|look\s*up|google|browse|latest|current\s+(price|news|weather|status|value)|weather\s+(in|for|at)|news\s+(about|on|from)|price\s+of|how\s+much\s+(is|does|are)|what\s+(is|are)\s+the\s+(latest|current|today))\b/i],
	['tasks', /\b(tasks?|todo|to-do|assign|backlog|deadline|kanban|sprint|ticket|issue|work\s*item)\b/i],
	['calendar', /\b(calendar|event|meeting|schedule|appointment|agenda|upcoming|next\s+week|tomorrow|today)\b/i],
	['documents', /\b(documents?|docs?|notes?|knowledge|article|wiki|file|write\s+up|summary)\b/i],
	['messages', /\b(said|mentioned|conversation|discussed|talked\s+about|chat\s+history|who\s+said|search\s+messages?)\b/i],
];

/**
 * Classify user message into intents. Returns array of matched intents,
 * or ['general'] if no specific intent is detected.
 */
export function classifyIntent(message: string): Intent[] {
	const matched: Intent[] = [];
	for (const [intent, regex] of patterns) {
		if (regex.test(message)) {
			matched.push(intent);
		}
	}
	return matched.length > 0 ? matched : ['general'];
}
