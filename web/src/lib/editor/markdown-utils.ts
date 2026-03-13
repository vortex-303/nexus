import TurndownService from 'turndown';
import { marked } from 'marked';

const turndown = new TurndownService({
	headingStyle: 'atx',
	codeBlockStyle: 'fenced',
	bulletListMarker: '-',
});

// Improve turndown for task lists
turndown.addRule('taskListItem', {
	filter: (node) => {
		return node.nodeName === 'LI' && node.getAttribute('data-type') === 'taskItem';
	},
	replacement: (content, node) => {
		const checked = (node as HTMLElement).getAttribute('data-checked') === 'true';
		return `${checked ? '- [x]' : '- [ ]'} ${content.trim()}\n`;
	},
});

export function htmlToMarkdown(html: string): string {
	if (!html || !html.trim()) return '';
	return turndown.turndown(html);
}

export function markdownToHtml(md: string): string {
	if (!md || !md.trim()) return '';
	return marked.parse(md, { async: false }) as string;
}

// Safe version: escapes raw HTML before parsing markdown, preventing XSS.
// Also enables breaks (newlines → <br>) for chat messages.
export function safeMarkdownToHtml(md: string): string {
	if (!md || !md.trim()) return '';
	const escaped = md.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
	return marked.parse(escaped, { async: false, breaks: true }) as string;
}
