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
