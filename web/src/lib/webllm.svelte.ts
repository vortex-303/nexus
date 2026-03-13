import { CreateWebWorkerMLCEngine, type WebWorkerMLCEngine, prebuiltAppConfig, hasModelInCache, deleteModelAllInfoInCache } from '@mlc-ai/web-llm';

export const MAX_INSTALLED = 3;

// Reactive state
let currentModel = $state('');
let downloadProgress = $state(0);
let downloadStatus = $state('');
let isDownloading = $state(false);
let isLoaded = $state(false);
let isGenerating = $state(false);
let installedModels = $state<string[]>([]);

let engine: WebWorkerMLCEngine | null = null;
let worker: Worker | null = null;

export function getState() {
	return {
		get currentModel() { return currentModel; },
		get downloadProgress() { return downloadProgress; },
		get downloadStatus() { return downloadStatus; },
		get isDownloading() { return isDownloading; },
		get isLoaded() { return isLoaded; },
		get isGenerating() { return isGenerating; },
		get installedModels() { return installedModels; },
	};
}

export function hasWebGPU(): boolean {
	return typeof navigator !== 'undefined' && !!(navigator as any).gpu;
}

const recommendedList: { model_id: string; label: string; size: string }[] = [
	// Tool calling (via JSON schema constrained decoding)
	{ model_id: 'Qwen3-4B-q4f16_1-MLC', label: 'Qwen3 4B', size: '~3.4 GB' },
	{ model_id: 'Qwen3-1.7B-q4f16_1-MLC', label: 'Qwen3 1.7B', size: '~2.0 GB' },
	{ model_id: 'Qwen3-8B-q4f16_1-MLC', label: 'Qwen3 8B', size: '~5.7 GB' },
	// Chat-only
	{ model_id: 'Qwen2.5-1.5B-Instruct-q4f16_1-MLC', label: 'Qwen 2.5 1.5B', size: '~1.6 GB' },
	{ model_id: 'Qwen2.5-0.5B-Instruct-q4f16_1-MLC', label: 'Qwen 2.5 0.5B', size: '~0.9 GB' },
	{ model_id: 'SmolLM2-1.7B-Instruct-q4f16_1-MLC', label: 'SmolLM2 1.7B', size: '~1.7 GB' },
	{ model_id: 'Llama-3.2-3B-Instruct-q4f16_1-MLC', label: 'Llama 3.2 3B', size: '~2.2 GB' },
	{ model_id: 'Hermes-3-Llama-3.2-3B-q4f16_1-MLC', label: 'Hermes 3 3B', size: '~2.3 GB' },
	{ model_id: 'gemma-2b-it-q4f16_1-MLC', label: 'Gemma 2B', size: '~1.4 GB' },
];

// Models that support tool calling via response_format JSON schema
// Qwen3 models excel at tool calling in benchmarks
const toolCapableModels = new Set([
	'Qwen3-0.6B-q4f16_1-MLC',
	'Qwen3-0.6B-q4f32_1-MLC',
	'Qwen3-1.7B-q4f16_1-MLC',
	'Qwen3-1.7B-q4f32_1-MLC',
	'Qwen3-4B-q4f16_1-MLC',
	'Qwen3-4B-q4f32_1-MLC',
	'Qwen3-8B-q4f16_1-MLC',
	'Qwen3-8B-q4f32_1-MLC',
	'Llama-3.2-3B-Instruct-q4f16_1-MLC',
	'Hermes-3-Llama-3.2-3B-q4f16_1-MLC',
	'Hermes-3-Llama-3.1-8B-q4f16_1-MLC',
	'Hermes-2-Pro-Llama-3-8B-q4f16_1-MLC',
	'Hermes-2-Pro-Mistral-7B-q4f16_1-MLC',
]);

export function isToolCallingModel(modelId: string): boolean {
	return toolCapableModels.has(modelId);
}

export function getRecommendedModels(): { model_id: string; label: string; size: string; hasTools: boolean }[] {
	return recommendedList.map(m => ({
		...m,
		hasTools: toolCapableModels.has(m.model_id),
	}));
}

export function getAvailableModels(): { model_id: string; display_name: string; vram_required_MB?: number; hasTools: boolean }[] {
	const recommendedIds = new Set(recommendedList.map(m => m.model_id));
	return prebuiltAppConfig.model_list
		.filter(m => {
			if (recommendedIds.has(m.model_id)) return false;
			const id = m.model_id.toLowerCase();
			return (id.includes('chat') || id.includes('instruct') || id.includes('smollm') || id.includes('llama') || id.includes('gemma') || id.includes('phi') || id.includes('qwen') || id.includes('mistral'));
		})
		.map(m => ({
			model_id: m.model_id,
			display_name: m.model_id.split('/').pop()?.replace(/-/g, ' ') || m.model_id,
			vram_required_MB: m.vram_required_MB,
			hasTools: toolCapableModels.has(m.model_id),
		}))
		.sort((a, b) => (a.vram_required_MB || 0) - (b.vram_required_MB || 0));
}

export function getModelDisplayName(modelId: string): string {
	const rec = recommendedList.find(m => m.model_id === modelId);
	if (rec) return rec.label;
	return modelId.split('/').pop()?.replace(/-/g, ' ') || modelId;
}

/** Check which recommended + all known models are cached in the browser. */
export async function refreshInstalledModels(): Promise<string[]> {
	const allIds = [
		...recommendedList.map(m => m.model_id),
		...prebuiltAppConfig.model_list.map(m => m.model_id),
	];
	// Deduplicate
	const unique = [...new Set(allIds)];
	const results: string[] = [];
	for (const id of unique) {
		try {
			if (await hasModelInCache(id)) {
				results.push(id);
			}
		} catch {
			// ignore
		}
	}
	installedModels = results;
	return results;
}

export async function loadEngine(modelId: string): Promise<void> {
	if (isDownloading) return;

	// Check install limit — only if model is not already cached
	let alreadyCached = false;
	try {
		alreadyCached = await hasModelInCache(modelId);
	} catch {}

	if (!alreadyCached && installedModels.length >= MAX_INSTALLED) {
		throw new Error(`Maximum ${MAX_INSTALLED} models installed. Delete one first.`);
	}

	isDownloading = true;
	downloadProgress = 0;
	downloadStatus = 'Initializing...';

	try {
		if (engine) {
			await engine.unload();
			engine = null;
		}
		if (worker) {
			worker.terminate();
			worker = null;
		}

		worker = new Worker(new URL('./webllm-worker.ts', import.meta.url), { type: 'module' });

		engine = await CreateWebWorkerMLCEngine(worker, modelId, {
			initProgressCallback: (report) => {
				downloadProgress = report.progress;
				downloadStatus = report.text;
			},
		});

		currentModel = modelId;
		isLoaded = true;
		// Refresh installed list after potential new download
		await refreshInstalledModels();
	} catch (e) {
		console.error('WebLLM load failed:', e);
		downloadStatus = `Error: ${e}`;
		throw e;
	} finally {
		isDownloading = false;
	}
}

export async function unloadEngine(): Promise<void> {
	if (engine) {
		await engine.unload();
		engine = null;
	}
	if (worker) {
		worker.terminate();
		worker = null;
	}
	currentModel = '';
	isLoaded = false;
	downloadProgress = 0;
	downloadStatus = '';
}

export async function complete(
	systemPrompt: string,
	messages: { role: string; content: string }[]
): Promise<string> {
	if (!engine) throw new Error('WebLLM engine not loaded');
	isGenerating = true;
	try {
		const reply = await engine.chat.completions.create({
			messages: [
				{ role: 'system', content: systemPrompt },
				...messages,
			] as any,
		});
		return reply.choices[0]?.message?.content || '';
	} finally {
		isGenerating = false;
	}
}

export async function completeWithTools(
	systemPrompt: string,
	messages: { role: string; content: string }[],
	tools: any[],
	executeToolFn: (name: string, args: string) => Promise<string>,
	onToolCall?: (name: string) => void
): Promise<string> {
	if (!engine) throw new Error('WebLLM engine not loaded');
	isGenerating = true;
	try {
		// Round 1: LLM decides whether to call tools
		const allMessages: any[] = [
			{ role: 'system', content: systemPrompt },
			...messages,
		];
		const r1 = await engine.chat.completions.create({
			messages: allMessages,
			tools,
			tool_choice: 'auto',
		});
		const choice = r1.choices[0]?.message;
		if (!choice) return '';

		// No tool calls — return content directly
		if (!choice.tool_calls || choice.tool_calls.length === 0) {
			return choice.content || '';
		}

		// Execute each tool call
		const toolResults: any[] = [];
		for (const tc of choice.tool_calls) {
			const name = tc.function?.name || '';
			const args = tc.function?.arguments || '{}';
			onToolCall?.(name);
			const result = await executeToolFn(name, args);
			toolResults.push({
				role: 'tool',
				tool_call_id: tc.id,
				content: result,
			});
		}

		// Round 2: synthesize final response (no tools param — forces text)
		const r2Messages: any[] = [
			...allMessages,
			{ role: 'assistant', tool_calls: choice.tool_calls },
			...toolResults,
		];
		const r2 = await engine.chat.completions.create({
			messages: r2Messages,
		});
		return r2.choices[0]?.message?.content || '';
	} finally {
		isGenerating = false;
	}
}

export async function deleteModelCache(modelId: string): Promise<void> {
	if (currentModel === modelId && isLoaded) {
		throw new Error('Cannot delete the currently loaded model. Unload it first.');
	}
	await deleteModelAllInfoInCache(modelId);
	await refreshInstalledModels();
}
