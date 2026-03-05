<script lang="ts">
	let { file, slug, onclose }: { file: any; slug: string; onclose: () => void } = $props();

	function getFileUrl(hash: string): string {
		return `/api/workspaces/${slug}/files/${hash}`;
	}

	function isImage(mime: string): boolean { return mime?.startsWith('image/'); }
	function isVideo(mime: string): boolean { return mime?.startsWith('video/'); }
	function isAudio(mime: string): boolean { return mime?.startsWith('audio/'); }
	function isPDF(mime: string): boolean { return mime === 'application/pdf'; }
	function isText(mime: string): boolean {
		return mime?.startsWith('text/') || ['application/json', 'application/javascript', 'application/xml', 'application/yaml'].includes(mime);
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}
</script>

<div class="preview-panel">
	<div class="preview-header">
		<h3>{file.name}</h3>
		<button class="preview-close" onclick={onclose}>&times;</button>
	</div>

	<div class="preview-body">
		{#if isImage(file.mime)}
			<img src={getFileUrl(file.hash)} alt={file.name} class="preview-image" />
		{:else if isPDF(file.mime)}
			<iframe src={getFileUrl(file.hash)} class="preview-iframe" title={file.name}></iframe>
		{:else if isVideo(file.mime)}
			<video controls class="preview-video" src={getFileUrl(file.hash)}>
				<track kind="captions" />
			</video>
		{:else if isAudio(file.mime)}
			<div class="preview-audio-wrap">
				<audio controls src={getFileUrl(file.hash)}></audio>
			</div>
		{:else if isText(file.mime)}
			<div class="preview-text-loading">Preview not available for this file type.</div>
		{:else}
			<div class="preview-fallback">
				<svg width="48" height="48" viewBox="0 0 48 48" fill="none">
					<path d="M12 4h16l12 12v24a4 4 0 01-4 4H12a4 4 0 01-4-4V8a4 4 0 014-4z" stroke="var(--text-tertiary)" stroke-width="1.5" fill="none"/>
					<path d="M28 4v12h12" stroke="var(--text-tertiary)" stroke-width="1.5"/>
				</svg>
				<p>No preview available</p>
			</div>
		{/if}
	</div>

	<div class="preview-meta">
		<div class="meta-row">
			<span class="meta-label">Type</span>
			<span class="meta-value">{file.mime}</span>
		</div>
		<div class="meta-row">
			<span class="meta-label">Size</span>
			<span class="meta-value">{formatSize(file.size)}</span>
		</div>
		<div class="meta-row">
			<span class="meta-label">Uploaded</span>
			<span class="meta-value">{new Date(file.created_at).toLocaleDateString()}</span>
		</div>
		<a href={getFileUrl(file.hash)} download={file.name} class="btn-download">Download</a>
	</div>
</div>

<style>
	.preview-panel {
		width: 380px;
		border-left: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		display: flex;
		flex-direction: column;
		overflow: hidden;
		flex-shrink: 0;
	}
	.preview-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-lg);
		border-bottom: 1px solid var(--border-subtle);
	}
	.preview-header h3 {
		font-size: var(--text-sm);
		font-weight: 600;
		margin: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		flex: 1;
	}
	.preview-close {
		background: none;
		border: none;
		color: var(--text-secondary);
		font-size: 18px;
		cursor: pointer;
		flex-shrink: 0;
	}
	.preview-body {
		flex: 1;
		overflow: auto;
		display: flex;
		align-items: center;
		justify-content: center;
		padding: var(--space-md);
	}
	.preview-image {
		max-width: 100%;
		max-height: 100%;
		object-fit: contain;
		border-radius: var(--radius-md);
	}
	.preview-iframe {
		width: 100%;
		height: 100%;
		border: none;
		border-radius: var(--radius-md);
	}
	.preview-video {
		max-width: 100%;
		border-radius: var(--radius-md);
	}
	.preview-audio-wrap {
		padding: var(--space-xl);
	}
	.preview-audio-wrap audio {
		width: 100%;
	}
	.preview-fallback {
		text-align: center;
		color: var(--text-tertiary);
	}
	.preview-fallback p { margin-top: var(--space-md); font-size: var(--text-sm); }
	.preview-text-loading {
		color: var(--text-tertiary);
		font-size: var(--text-sm);
	}
	.preview-meta {
		padding: var(--space-md) var(--space-lg);
		border-top: 1px solid var(--border-subtle);
		display: flex;
		flex-direction: column;
		gap: var(--space-sm);
	}
	.meta-row {
		display: flex;
		justify-content: space-between;
		font-size: var(--text-xs);
	}
	.meta-label { color: var(--text-tertiary); }
	.meta-value { color: var(--text-secondary); }
	.btn-download {
		display: block;
		text-align: center;
		padding: 8px;
		background: var(--accent);
		color: var(--text-inverse);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 600;
		text-decoration: none;
		margin-top: var(--space-sm);
	}
	.btn-download:hover { background: var(--accent-hover); }
</style>
