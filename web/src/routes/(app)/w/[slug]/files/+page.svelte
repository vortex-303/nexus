<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { getCurrentUser, listFolders, createFolder, updateFolder, deleteFolder, uploadToFolder, updateFile, moveFile, moveDoc, deleteFile, duplicateFile, fileUrl, createKnowledge, createDoc, updateDoc, deleteDoc } from '$lib/api';
	import FilePreview from '$lib/components/FilePreview.svelte';
	import TiptapEditor from '$lib/editor/TiptapEditor.svelte';
	import MarkdownEditor from '$lib/editor/MarkdownEditor.svelte';
	import { htmlToMarkdown, markdownToHtml } from '$lib/editor/markdown-utils';
	import { members } from '$lib/stores/workspace';
	import { onDestroy } from 'svelte';

	let slug = $derived(page.params.slug);
	let currentUser = $state(getCurrentUser());

	type ViewMode = 'grid' | 'list';
	type SortMode = 'date_desc' | 'date_asc' | 'name_asc' | 'name_desc' | 'size_desc';
	let viewMode = $state<ViewMode>('grid');
	let sortMode = $state<SortMode>('date_desc');
	let uploaderFilter = $state<'all' | 'my'>('all');

	let membersList: any[] = [];
	const unsubMembers = members.subscribe(v => membersList = v);

	function getFileMember(id: string) {
		return membersList.find((m: any) => m.id === id);
	}
	function fileMemberInitial(m: any) {
		return (m?.display_name || '?')[0].toUpperCase();
	}
	function fileMemberColor(m: any) {
		return m?.color || 'var(--text-tertiary)';
	}
	let folders = $state<any[]>([]);
	let files = $state<any[]>([]);
	let docs = $state<any[]>([]);
	let breadcrumbs = $state<{ id: string; name: string }[]>([]);
	let currentFolderId = $state('');
	let selectedFile = $state<any>(null);
	let dragOver = $state(false);
	let uploading = $state(false);
	let showNewFolder = $state(false);
	let newFolderName = $state('');

	// Note editor state
	let editingDoc = $state<any>(null);
	let docTitle = $state('');
	let docContent = $state('');
	let docSaving = $state(false);
	let editorRef = $state<TiptapEditor>();
	let mdEditorRef = $state<MarkdownEditor>();
	let markdownMode = $state(false);
	let markdownContent = $state('');
	let saveTimer: any = null;

	// Selection
	let selectedIds = $state<Set<string>>(new Set());

	// Ellipsis menu (per-item)
	let ellipsisMenu = $state<{ id: string; type: 'file' | 'folder' | 'doc'; item: any; x: number; y: number } | null>(null);

	// Context menu (right-click)
	let contextMenu = $state<{ x: number; y: number; type: 'file' | 'folder' | 'doc'; item: any } | null>(null);
	let renaming = $state<{ id: string; type: 'file' | 'folder' | 'doc'; name: string } | null>(null);

	// Drag-to-folder state
	let dragTargetFolderId = $state<string | null>(null);

	// Resizable editor panel
	let editorWidth = $state(520);
	let resizing = $state(false);

	// "Move to" dropdown
	let showMoveDropdown = $state(false);
	let allRootFolders = $state<any[]>([]);

	let fileInputEl: HTMLInputElement;

	onMount(async () => {
		const saved = localStorage.getItem('nexus_files_view');
		if (saved === 'list' || saved === 'grid') viewMode = saved;
		const savedMode = localStorage.getItem('nexus_editor_mode');
		if (savedMode === 'markdown') markdownMode = true;
		const savedWidth = localStorage.getItem('nexus_editor_width');
		if (savedWidth) editorWidth = Math.max(320, Math.min(900, parseInt(savedWidth)));
		const savedSort = localStorage.getItem('nexus_files_sort');
		if (savedSort) sortMode = savedSort as SortMode;
		const savedFilter = localStorage.getItem('nexus_files_uploader');
		if (savedFilter === 'my') uploaderFilter = 'my';
		await loadFolder('');
	});

	onDestroy(() => {
		unsubMembers();
	});

	let displayFiles = $derived(uploaderFilter === 'my' ? files.filter(f => f.uploader_id === currentUser?.uid) : files);

	function toggleUploaderFilter() {
		uploaderFilter = uploaderFilter === 'all' ? 'my' : 'all';
		localStorage.setItem('nexus_files_uploader', uploaderFilter);
	}

	function applySorting() {
		const cmp = (a: any, b: any): number => {
			switch (sortMode) {
				case 'date_desc': return new Date(b.updated_at || b.created_at).getTime() - new Date(a.updated_at || a.created_at).getTime();
				case 'date_asc': return new Date(a.updated_at || a.created_at).getTime() - new Date(b.updated_at || b.created_at).getTime();
				case 'name_asc': return (a.name || a.title || '').localeCompare(b.name || b.title || '');
				case 'name_desc': return (b.name || b.title || '').localeCompare(a.name || a.title || '');
				case 'size_desc': return (b.size || 0) - (a.size || 0);
				default: return 0;
			}
		};
		folders = [...folders].sort((a, b) => (a.name || '').localeCompare(b.name || '') || cmp(a, b));
		docs = [...docs].sort(cmp);
		files = [...files].sort(cmp);
	}

	function setSortMode(mode: SortMode) {
		sortMode = mode;
		localStorage.setItem('nexus_files_sort', mode);
		applySorting();
	}

	async function loadFolder(folderId: string) {
		try {
			const data = await listFolders(slug, folderId || undefined);
			folders = data.folders || [];
			files = data.files || [];
			docs = data.documents || [];
			currentFolderId = folderId;
			selectedFile = null;
			selectedIds = new Set();
			applySorting();
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function navigateToFolder(folderId: string, folderName: string) {
		if (folderId) {
			const idx = breadcrumbs.findIndex(b => b.id === folderId);
			if (idx >= 0) {
				breadcrumbs = breadcrumbs.slice(0, idx + 1);
			} else {
				breadcrumbs = [...breadcrumbs, { id: folderId, name: folderName }];
			}
		} else {
			breadcrumbs = [];
		}
		await loadFolder(folderId);
	}

	async function handleCreateFolder() {
		if (!newFolderName.trim()) return;
		try {
			await createFolder(slug, {
				name: newFolderName.trim(),
				parent_id: currentFolderId || undefined,
			});
			newFolderName = '';
			showNewFolder = false;
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleCreateNote() {
		try {
			const doc = await createDoc(slug, {
				title: 'Untitled',
				content: '',
				folder_id: currentFolderId || undefined,
			});
			await loadFolder(currentFolderId);
			openNoteEditor(doc);
		} catch (e: any) {
			alert(e.message);
		}
	}

	function openNoteEditor(doc: any) {
		editingDoc = doc;
		docTitle = doc.title || '';
		docContent = doc.content || '';
		selectedFile = null;
		if (markdownMode) {
			markdownContent = htmlToMarkdown(doc.content || '');
			requestAnimationFrame(() => {
				if (mdEditorRef) mdEditorRef.setContent(markdownContent);
			});
		}
		// Rich text mode uses initialContent prop + {#key} to re-mount with content
	}

	function closeNoteEditor() {
		if (saveTimer) { clearTimeout(saveTimer); saveTimer = null; }
		// Final save
		if (editingDoc) {
			saveDoc();
		}
		editingDoc = null;
		docTitle = '';
		docContent = '';
	}

	async function saveDoc() {
		if (!editingDoc) return;
		docSaving = true;
		try {
			const updated = await updateDoc(slug, editingDoc.id, { title: docTitle, content: docContent });
			editingDoc = updated;
			docs = docs.map(d => d.id === updated.id ? updated : d);
		} catch (e: any) {
			console.error('Save failed:', e.message);
		} finally {
			docSaving = false;
		}
	}

	function handleAutoSave(html: string) {
		if (!editingDoc) return;
		docContent = html;
		if (saveTimer) clearTimeout(saveTimer);
		saveTimer = setTimeout(() => {
			saveDoc();
		}, 1500);
	}

	async function handleDeleteNote(doc: any) {
		if (!confirm(`Delete note "${doc.title || 'Untitled'}"?`)) return;
		closeMenus();
		try {
			await deleteDoc(slug, doc.id);
			if (editingDoc?.id === doc.id) {
				editingDoc = null;
				docTitle = '';
				docContent = '';
			}
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	function toggleEditorMode() {
		if (!markdownMode) {
			const html = editorRef?.getHTML() || docContent;
			markdownContent = htmlToMarkdown(html);
			markdownMode = true;
			localStorage.setItem('nexus_editor_mode', 'markdown');
		} else {
			const md = mdEditorRef?.getContent() || markdownContent;
			const html = markdownToHtml(md);
			markdownMode = false;
			docContent = html;
			localStorage.setItem('nexus_editor_mode', 'rich');
			requestAnimationFrame(() => {
				if (editorRef) editorRef.setContent(html);
			});
		}
	}

	function startResize(e: MouseEvent) {
		e.preventDefault();
		resizing = true;
		const startX = e.clientX;
		const startWidth = editorWidth;

		function onMove(e: MouseEvent) {
			const delta = startX - e.clientX;
			editorWidth = Math.max(320, Math.min(900, startWidth + delta));
		}

		function onUp() {
			resizing = false;
			localStorage.setItem('nexus_editor_width', String(editorWidth));
			window.removeEventListener('mousemove', onMove);
			window.removeEventListener('mouseup', onUp);
		}

		window.addEventListener('mousemove', onMove);
		window.addEventListener('mouseup', onUp);
	}

	async function handleUpload(fileList: FileList) {
		if (!fileList.length) return;
		uploading = true;
		try {
			for (const f of fileList) {
				await uploadToFolder(slug, currentFolderId || '_root', f);
			}
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
		uploading = false;
	}

	function handleFileInput(e: Event) {
		const input = e.target as HTMLInputElement;
		if (input.files) handleUpload(input.files);
		input.value = '';
	}

	function handleDragOverZone(e: DragEvent) { e.preventDefault(); dragOver = true; }
	function handleDragLeaveZone() { dragOver = false; }
	function handleDropZone(e: DragEvent) {
		e.preventDefault();
		dragOver = false;
		if (e.dataTransfer?.files) handleUpload(e.dataTransfer.files);
	}

	// Selection toggling
	function toggleSelect(id: string, e: MouseEvent) {
		e.stopPropagation();
		const next = new Set(selectedIds);
		if (next.has(id)) next.delete(id); else next.add(id);
		selectedIds = next;
	}

	function selectAll() {
		selectedIds = new Set([...folders.map(f => f.id), ...files.map(f => f.id), ...docs.map(d => d.id)]);
	}

	function clearSelection() {
		selectedIds = new Set();
	}

	// Bulk actions
	async function bulkDelete() {
		if (!selectedIds.size) return;
		if (!confirm(`Delete ${selectedIds.size} item(s)?`)) return;
		try {
			for (const id of selectedIds) {
				const isFolder = folders.some(f => f.id === id);
				const isDoc = docs.some(d => d.id === id);
				if (isFolder) {
					try { await deleteFolder(slug, id); }
					catch { await deleteFolder(slug, id, true); }
				}
				else if (isDoc) await deleteDoc(slug, id);
				else await deleteFile(slug, id);
			}
			selectedIds = new Set();
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	// Ellipsis menu
	function showEllipsis(e: MouseEvent, type: 'file' | 'folder' | 'doc', item: any) {
		e.stopPropagation();
		e.preventDefault();
		const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
		ellipsisMenu = { id: item.id, type, item, x: rect.right, y: rect.bottom + 4 };
	}

	function closeMenus() {
		ellipsisMenu = null;
		contextMenu = null;
		showMoveDropdown = false;
	}

	// Context menu (right-click)
	function showContext(e: MouseEvent, type: 'file' | 'folder' | 'doc', item: any) {
		e.preventDefault();
		contextMenu = { x: e.clientX, y: e.clientY, type, item };
	}

	function startRename(type: 'file' | 'folder' | 'doc', item: any) {
		renaming = { id: item.id, type, name: type === 'doc' ? item.title : item.name };
		closeMenus();
	}

	async function submitRename() {
		if (!renaming) return;
		try {
			if (renaming.type === 'folder') {
				await updateFolder(slug, renaming.id, { name: renaming.name });
			} else if (renaming.type === 'doc') {
				await updateDoc(slug, renaming.id, { title: renaming.name });
			} else {
				await updateFile(slug, renaming.id, { name: renaming.name });
			}
			renaming = null;
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDelete(type: 'file' | 'folder' | 'doc', item: any) {
		const label = type === 'doc' ? (item.title || 'Untitled') : item.name;
		if (!confirm(`Delete ${type === 'doc' ? 'note' : type} "${label}"?`)) return;
		closeMenus();
		try {
			if (type === 'folder') {
				try {
					await deleteFolder(slug, item.id);
				} catch (err: any) {
					if (err.message?.includes('contains') && confirm(`Folder "${item.name}" is not empty. Delete everything inside it?`)) {
						await deleteFolder(slug, item.id, true);
					} else {
						throw err;
					}
				}
			} else if (type === 'doc') {
				await deleteDoc(slug, item.id);
				if (editingDoc?.id === item.id) { editingDoc = null; docTitle = ''; docContent = ''; }
			}
			else await deleteFile(slug, item.id);
			await loadFolder(currentFolderId);
			if (selectedFile?.id === item.id) selectedFile = null;
		} catch (e: any) {
			alert(e.message);
		}
	}

	async function handleDuplicate(item: any) {
		closeMenus();
		try {
			await duplicateFile(slug, item.id);
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}

	function handleDownload(item: any) {
		closeMenus();
		const a = document.createElement('a');
		a.href = fileUrl(slug, item.hash);
		a.download = item.name;
		a.click();
	}

	function handleShare(item: any) {
		closeMenus();
		const url = `${location.origin}${fileUrl(slug, item.hash)}`;
		navigator.clipboard.writeText(url).then(() => {
			alert('Link copied to clipboard');
		});
	}

	async function handleAddToKnowledge(item: any) {
		closeMenus();
		try {
			const resp = await fetch(fileUrl(slug, item.hash));
			const text = await resp.text();
			await createKnowledge(slug, { title: item.name, content: text });
			alert('Added to Knowledge Base');
		} catch (e: any) {
			alert('Failed to add: ' + e.message);
		}
	}

	function setViewMode(mode: ViewMode) {
		viewMode = mode;
		localStorage.setItem('nexus_files_view', mode);
	}

	function formatSize(bytes: number): string {
		if (bytes < 1024) return bytes + ' B';
		if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
		return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
	}

	function isImage(mime: string): boolean { return mime?.startsWith('image/'); }

	function getIcon(mime: string): string {
		if (mime?.startsWith('image/')) return '🖼';
		if (mime?.startsWith('video/')) return '🎬';
		if (mime?.startsWith('audio/')) return '🎵';
		if (mime === 'application/pdf') return '📄';
		if (mime?.startsWith('text/')) return '📝';
		return '📎';
	}

	function formatRelative(iso: string) {
		const d = new Date(iso);
		const now = new Date();
		const diff = now.getTime() - d.getTime();
		if (diff < 60000) return 'just now';
		if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`;
		if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`;
		return d.toLocaleDateString([], { month: 'short', day: 'numeric' });
	}

	// Drag-to-folder: item drag start
	function handleItemDragStart(e: DragEvent, type: 'file' | 'doc', id: string) {
		e.dataTransfer!.setData('application/json', JSON.stringify({ type, id }));
		e.dataTransfer!.effectAllowed = 'move';
	}

	function handleItemDragEnd() {
		dragTargetFolderId = null;
		dragOver = false;
	}

	// Drag-to-folder: folder drop target handlers
	function handleFolderDragOver(e: DragEvent, folderId: string) {
		const data = e.dataTransfer?.types.includes('application/json');
		if (!data) return;
		e.preventDefault();
		e.dataTransfer!.dropEffect = 'move';
		dragTargetFolderId = folderId;
	}

	function handleFolderDragLeave(e: DragEvent, folderId: string) {
		if (dragTargetFolderId === folderId) dragTargetFolderId = null;
	}

	async function handleFolderDrop(e: DragEvent, targetFolderId: string) {
		e.preventDefault();
		e.stopPropagation();
		dragTargetFolderId = null;
		const raw = e.dataTransfer?.getData('application/json');
		if (!raw) return;
		try {
			const { type, id: itemId } = JSON.parse(raw);
			if (type === 'file') await moveFile(slug, itemId, targetFolderId);
			else if (type === 'doc') await moveDoc(slug, itemId, targetFolderId);
			await loadFolder(currentFolderId);
		} catch (err: any) {
			alert(err.message);
		}
	}

	// Breadcrumb drop (move to root or parent)
	async function handleBreadcrumbDrop(e: DragEvent, folderId: string) {
		e.preventDefault();
		e.stopPropagation();
		dragTargetFolderId = null;
		const raw = e.dataTransfer?.getData('application/json');
		if (!raw) return;
		try {
			const { type, id: itemId } = JSON.parse(raw);
			if (type === 'file') await moveFile(slug, itemId, folderId || '');
			else if (type === 'doc') await moveDoc(slug, itemId, folderId || '');
			await loadFolder(currentFolderId);
		} catch (err: any) {
			alert(err.message);
		}
	}

	// "Move to" dropdown
	async function openMoveDropdown() {
		try {
			const data = await listFolders(slug);
			allRootFolders = data.folders || [];
		} catch {}
		showMoveDropdown = true;
	}

	async function bulkMoveTo(targetFolderId: string) {
		showMoveDropdown = false;
		try {
			for (const itemId of selectedIds) {
				const isDoc = docs.some(d => d.id === itemId);
				const isFolder = folders.some(f => f.id === itemId);
				if (isFolder) continue; // don't move folders via this
				if (isDoc) await moveDoc(slug, itemId, targetFolderId);
				else await moveFile(slug, itemId, targetFolderId);
			}
			selectedIds = new Set();
			await loadFolder(currentFolderId);
		} catch (e: any) {
			alert(e.message);
		}
	}
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="files-page" onclick={closeMenus}>
	<header class="files-header">
		<div class="header-left">
			<button class="back-btn" onclick={() => goto(`/w/${slug}`)}>
				<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
					<path d="M10 3L5 8L10 13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
				</svg>
			</button>
			<h1>Files</h1>
		</div>
		<div class="header-right">
			{#if selectedIds.size > 0}
				<span class="selection-count">{selectedIds.size} selected</span>
				<div class="move-to-wrapper">
					<button class="btn-action" onclick={openMoveDropdown}>Move to</button>
					{#if showMoveDropdown}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div class="move-dropdown" onclick={(e) => e.stopPropagation()}>
							<button onclick={() => bulkMoveTo('')}>Root</button>
							{#each allRootFolders as f}
								<button onclick={() => bulkMoveTo(f.id)}>{f.name}</button>
							{/each}
						</div>
					{/if}
				</div>
				<button class="btn-action" onclick={bulkDelete}>Delete Selected</button>
				<button class="btn-action" onclick={clearSelection}>Clear</button>
			{:else}
				<button class="btn-action btn-select-all" onclick={selectAll}>Select All</button>
			{/if}
			<button class="uploader-filter-btn" class:active={uploaderFilter === 'my'} onclick={toggleUploaderFilter}>
				{uploaderFilter === 'my' ? 'My Uploads' : 'All Files'}
			</button>
			<select class="sort-select" value={sortMode} onchange={(e) => setSortMode((e.target as HTMLSelectElement).value as SortMode)}>
				<option value="date_desc">Newest first</option>
				<option value="date_asc">Oldest first</option>
				<option value="name_asc">Name A-Z</option>
				<option value="name_desc">Name Z-A</option>
				<option value="size_desc">Largest first</option>
			</select>
			<div class="view-toggle">
				<button class="toggle-btn" class:active={viewMode === 'grid'} onclick={() => setViewMode('grid')} title="Grid view">
					<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
						<rect x="1" y="1" width="6" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
						<rect x="9" y="1" width="6" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
						<rect x="1" y="9" width="6" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
						<rect x="9" y="9" width="6" height="6" rx="1" stroke="currentColor" stroke-width="1.2"/>
					</svg>
				</button>
				<button class="toggle-btn" class:active={viewMode === 'list'} onclick={() => setViewMode('list')} title="List view">
					<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
						<path d="M2 4H14M2 8H14M2 12H14" stroke="currentColor" stroke-width="1.2" stroke-linecap="round"/>
					</svg>
				</button>
			</div>
			<button class="btn-action" onclick={() => showNewFolder = true}>New Folder</button>
			<button class="btn-action" onclick={handleCreateNote}>New Note</button>
			<button class="btn-upload" onclick={() => fileInputEl.click()}>Upload</button>
			<input bind:this={fileInputEl} type="file" multiple hidden onchange={handleFileInput} />
		</div>
	</header>

	<nav class="breadcrumbs">
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<button
			class="crumb"
			class:active={!currentFolderId}
			class:drop-highlight={dragTargetFolderId === '_root'}
			onclick={() => navigateToFolder('', '')}
			ondragover={(e) => handleFolderDragOver(e, '_root')}
			ondragleave={(e) => handleFolderDragLeave(e, '_root')}
			ondrop={(e) => handleBreadcrumbDrop(e, '')}
		>Files</button>
		{#each breadcrumbs as crumb}
			<span class="crumb-sep">/</span>
			<!-- svelte-ignore a11y_no_static_element_interactions -->
			<button
				class="crumb"
				class:active={currentFolderId === crumb.id}
				class:drop-highlight={dragTargetFolderId === crumb.id}
				onclick={() => navigateToFolder(crumb.id, crumb.name)}
				ondragover={(e) => handleFolderDragOver(e, crumb.id)}
				ondragleave={(e) => handleFolderDragLeave(e, crumb.id)}
				ondrop={(e) => handleBreadcrumbDrop(e, crumb.id)}
			>{crumb.name}</button>
		{/each}
	</nav>

	{#if showNewFolder}
		<div class="new-folder-bar">
			<input type="text" placeholder="Folder name..." bind:value={newFolderName} onkeydown={(e) => e.key === 'Enter' && handleCreateFolder()} />
			<button class="btn-create" onclick={handleCreateFolder}>Create</button>
			<button class="btn-cancel" onclick={() => { showNewFolder = false; newFolderName = ''; }}>Cancel</button>
		</div>
	{/if}

	<div class="files-content">
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div
			class="files-area"
			class:drag-over={dragOver}
			ondragover={handleDragOverZone}
			ondragleave={handleDragLeaveZone}
			ondrop={handleDropZone}
		>
			{#if uploading}
				<div class="upload-overlay">Uploading...</div>
			{/if}

			{#if viewMode === 'grid'}
				<div class="grid">
					{#each folders as folder}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="grid-item folder-item"
							class:selected={selectedIds.has(folder.id)}
							class:drop-highlight={dragTargetFolderId === folder.id}
							ondblclick={() => navigateToFolder(folder.id, folder.name)}
							oncontextmenu={(e) => showContext(e, 'folder', folder)}
							ondragover={(e) => handleFolderDragOver(e, folder.id)}
							ondragleave={(e) => handleFolderDragLeave(e, folder.id)}
							ondrop={(e) => handleFolderDrop(e, folder.id)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div class="item-checkbox" onclick={(e) => toggleSelect(folder.id, e)}>
								<input type="checkbox" checked={selectedIds.has(folder.id)} tabindex={-1} />
							</div>
							<button class="ellipsis-btn" onclick={(e) => showEllipsis(e, 'folder', folder)} title="Actions">
								<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
									<circle cx="7" cy="3" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="11" r="1.2" fill="currentColor"/>
								</svg>
							</button>
							{#if renaming?.id === folder.id}
								<input class="rename-input" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
							{:else}
								<div class="grid-icon folder-icon">
									<svg width="40" height="40" viewBox="0 0 40 40" fill="none">
										<path d="M4 10a2 2 0 012-2h10l4 4h14a2 2 0 012 2v18a2 2 0 01-2 2H6a2 2 0 01-2-2V10z" fill="var(--accent)" opacity="0.2" stroke="var(--accent)" stroke-width="1"/>
									</svg>
								</div>
								<span class="grid-name">{folder.name}</span>
								{#if folder.is_private}
									<span class="private-badge">Private</span>
								{/if}
							{/if}
						</div>
					{/each}
					{#each docs as doc}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="grid-item doc-item"
							class:selected={selectedIds.has(doc.id) || editingDoc?.id === doc.id}
							draggable="true"
							ondragstart={(e) => handleItemDragStart(e, 'doc', doc.id)} ondragend={handleItemDragEnd}
							onclick={() => openNoteEditor(doc)}
							oncontextmenu={(e) => showContext(e, 'doc', doc)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div class="item-checkbox" onclick={(e) => toggleSelect(doc.id, e)}>
								<input type="checkbox" checked={selectedIds.has(doc.id)} tabindex={-1} />
							</div>
							<button class="ellipsis-btn" onclick={(e) => showEllipsis(e, 'doc', doc)} title="Actions">
								<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
									<circle cx="7" cy="3" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="11" r="1.2" fill="currentColor"/>
								</svg>
							</button>
							{#if renaming?.id === doc.id}
								<input class="rename-input" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
							{:else}
								<div class="grid-icon doc-icon">
									<svg width="36" height="40" viewBox="0 0 36 40" fill="none">
										<path d="M4 4a2 2 0 012-2h16l10 10v24a2 2 0 01-2 2H6a2 2 0 01-2-2V4z" fill="var(--accent)" opacity="0.15" stroke="var(--accent)" stroke-width="1"/>
										<path d="M22 2v10h10" stroke="var(--accent)" stroke-width="1" opacity="0.4"/>
										<path d="M10 20h16M10 26h12M10 32h8" stroke="var(--accent)" stroke-width="1" opacity="0.3" stroke-linecap="round"/>
									</svg>
								</div>
								<span class="grid-name">{doc.title || 'Untitled'}</span>
								<span class="grid-size">{formatRelative(doc.updated_at || doc.created_at)}</span>
							{/if}
						</div>
					{/each}
					{#each displayFiles as file}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div
							class="grid-item file-item"
							class:selected={selectedIds.has(file.id) || selectedFile?.id === file.id}
							draggable="true"
							ondragstart={(e) => handleItemDragStart(e, 'file', file.id)} ondragend={handleItemDragEnd}
							onclick={() => { selectedFile = file; editingDoc = null; }}
							oncontextmenu={(e) => showContext(e, 'file', file)}
						>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<div class="item-checkbox" onclick={(e) => toggleSelect(file.id, e)}>
								<input type="checkbox" checked={selectedIds.has(file.id)} tabindex={-1} />
							</div>
							<button class="ellipsis-btn" onclick={(e) => showEllipsis(e, 'file', file)} title="Actions">
								<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
									<circle cx="7" cy="3" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
									<circle cx="7" cy="11" r="1.2" fill="currentColor"/>
								</svg>
							</button>
							{#if renaming?.id === file.id}
								<input class="rename-input" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
							{:else}
								{#if isImage(file.mime)}
									<div class="grid-thumbnail">
										<img src={fileUrl(slug, file.hash)} alt={file.name} />
									</div>
								{:else}
									<div class="grid-icon">{getIcon(file.mime)}</div>
								{/if}
								<span class="grid-name">{file.name}</span>
								<span class="grid-size">{formatSize(file.size)}</span>
							{/if}
						</div>
					{/each}

					{#if folders.length === 0 && files.length === 0 && docs.length === 0}
						<div class="empty-state">
							<svg width="48" height="48" viewBox="0 0 48 48" fill="none">
								<path d="M8 16a4 4 0 014-4h8l4 4h12a4 4 0 014 4v16a4 4 0 01-4 4H12a4 4 0 01-4-4V16z" stroke="var(--text-tertiary)" stroke-width="1.5" fill="none" opacity="0.3"/>
							</svg>
							<p>Drop files here, upload, or create a note</p>
						</div>
					{/if}
				</div>
			{:else}
				<div class="list-view">
					<div class="list-header">
						<span class="list-check-col"></span>
						<span class="list-name-col">Name</span>
						<span class="list-size-col">Size</span>
						<span class="list-type-col">Type</span>
						<span class="list-date-col">Modified</span>
						<span class="list-actions-col"></span>
					</div>
					{#each folders as folder}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div class="list-row" class:selected={selectedIds.has(folder.id)} class:drop-highlight={dragTargetFolderId === folder.id} ondblclick={() => navigateToFolder(folder.id, folder.name)} oncontextmenu={(e) => showContext(e, 'folder', folder)} ondragover={(e) => handleFolderDragOver(e, folder.id)} ondragleave={(e) => handleFolderDragLeave(e, folder.id)} ondrop={(e) => handleFolderDrop(e, folder.id)}>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<span class="list-check-col" onclick={(e) => toggleSelect(folder.id, e)}>
								<input type="checkbox" checked={selectedIds.has(folder.id)} tabindex={-1} />
							</span>
							<span class="list-name-col">
								<span class="list-icon">📁</span>
								{#if renaming?.id === folder.id}
									<input class="rename-input-inline" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
								{:else}
									{folder.name}
								{/if}
								{#if folder.is_private}<span class="private-badge">Private</span>{/if}
							</span>
							<span class="list-size-col">-</span>
							<span class="list-type-col">Folder</span>
							<span class="list-date-col">{new Date(folder.updated_at).toLocaleDateString()}</span>
							<span class="list-actions-col">
								<button class="ellipsis-btn-inline" onclick={(e) => showEllipsis(e, 'folder', folder)} title="Actions">
									<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
										<circle cx="3" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="11" cy="7" r="1.2" fill="currentColor"/>
									</svg>
								</button>
							</span>
						</div>
					{/each}
					{#each docs as doc}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div class="list-row" class:selected={selectedIds.has(doc.id) || editingDoc?.id === doc.id} draggable="true" ondragstart={(e) => handleItemDragStart(e, 'doc', doc.id)} ondragend={handleItemDragEnd} onclick={() => openNoteEditor(doc)} oncontextmenu={(e) => showContext(e, 'doc', doc)}>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<span class="list-check-col" onclick={(e) => toggleSelect(doc.id, e)}>
								<input type="checkbox" checked={selectedIds.has(doc.id)} tabindex={-1} />
							</span>
							<span class="list-name-col">
								<span class="list-icon">📝</span>
								{#if renaming?.id === doc.id}
									<input class="rename-input-inline" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
								{:else}
									{doc.title || 'Untitled'}
								{/if}
							</span>
							<span class="list-size-col">-</span>
							<span class="list-type-col">Note</span>
							<span class="list-date-col">{new Date(doc.updated_at || doc.created_at).toLocaleDateString()}</span>
							<span class="list-actions-col">
								<button class="ellipsis-btn-inline" onclick={(e) => showEllipsis(e, 'doc', doc)} title="Actions">
									<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
										<circle cx="3" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="11" cy="7" r="1.2" fill="currentColor"/>
									</svg>
								</button>
							</span>
						</div>
					{/each}
					{#each displayFiles as file}
						<!-- svelte-ignore a11y_no_static_element_interactions -->
						<div class="list-row" class:selected={selectedIds.has(file.id) || selectedFile?.id === file.id} draggable="true" ondragstart={(e) => handleItemDragStart(e, 'file', file.id)} ondragend={handleItemDragEnd} onclick={() => { selectedFile = file; editingDoc = null; }} oncontextmenu={(e) => showContext(e, 'file', file)}>
							<!-- svelte-ignore a11y_no_static_element_interactions -->
							<span class="list-check-col" onclick={(e) => toggleSelect(file.id, e)}>
								<input type="checkbox" checked={selectedIds.has(file.id)} tabindex={-1} />
							</span>
							<span class="list-name-col">
								{#if file.uploader_id}
									{@const uploader = getFileMember(file.uploader_id)}
									<span class="file-uploader-avatar" style="background: {fileMemberColor(uploader)}" title={uploader?.display_name || 'Unknown'}>{fileMemberInitial(uploader)}</span>
								{/if}
								<span class="list-icon">{getIcon(file.mime)}</span>
								{#if renaming?.id === file.id}
									<input class="rename-input-inline" type="text" bind:value={renaming.name} onkeydown={(e) => { if (e.key === 'Enter') submitRename(); if (e.key === 'Escape') renaming = null; }} />
								{:else}
									{file.name}
								{/if}
							</span>
							<span class="list-size-col">{formatSize(file.size)}</span>
							<span class="list-type-col">{file.mime?.split('/').pop()}</span>
							<span class="list-date-col">{new Date(file.created_at).toLocaleDateString()}</span>
							<span class="list-actions-col">
								<button class="ellipsis-btn-inline" onclick={(e) => showEllipsis(e, 'file', file)} title="Actions">
									<svg width="14" height="14" viewBox="0 0 14 14" fill="none">
										<circle cx="3" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="7" cy="7" r="1.2" fill="currentColor"/>
										<circle cx="11" cy="7" r="1.2" fill="currentColor"/>
									</svg>
								</button>
							</span>
						</div>
					{/each}
					{#if folders.length === 0 && files.length === 0 && docs.length === 0}
						<div class="empty-state">
							<p>No files, folders, or notes. Upload something or create a note.</p>
						</div>
					{/if}
				</div>
			{/if}
		</div>

		{#if selectedFile}
			<FilePreview file={selectedFile} {slug} onclose={() => selectedFile = null} />
		{/if}

		{#if editingDoc}
			<div class="note-editor-panel" class:resizing style="width: {editorWidth}px">
				<!-- svelte-ignore a11y_no_static_element_interactions -->
				<div class="resize-handle" onmousedown={startResize}></div>
				<div class="note-editor-header">
					<input
						type="text"
						class="note-title-input"
						placeholder="Note title..."
						bind:value={docTitle}
						onblur={saveDoc}
					/>
					<div class="note-editor-actions">
						<span class="note-saving" class:visible={docSaving}>Saving...</span>
						<button
							class="toolbar-btn"
							class:md-active={markdownMode}
							onclick={toggleEditorMode}
							title={markdownMode ? 'Switch to Rich Text' : 'Switch to Markdown'}
						>
							{markdownMode ? 'Rich Text' : 'Markdown'}
						</button>
						<button class="toolbar-btn" onclick={saveDoc} disabled={docSaving}>Save</button>
						<button class="toolbar-btn danger" onclick={() => handleDeleteNote(editingDoc)}>Delete</button>
						<button class="toolbar-btn" onclick={closeNoteEditor}>Close</button>
					</div>
				</div>
				<div class="note-editor-body">
					{#if markdownMode}
						<MarkdownEditor
							bind:this={mdEditorRef}
							content={markdownContent}
							onchange={(md) => {
								markdownContent = md;
								const html = markdownToHtml(md);
								handleAutoSave(html);
							}}
						/>
					{:else}
						{#key editingDoc?.id}
							<TiptapEditor
								bind:this={editorRef}
								onsave={handleAutoSave}
								placeholder="Start writing..."
								initialContent={docContent}
							/>
						{/key}
					{/if}
				</div>
			</div>
		{/if}
	</div>

	<!-- Ellipsis dropdown menu -->
	{#if ellipsisMenu}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="dropdown-menu" style="left: {ellipsisMenu.x}px; top: {ellipsisMenu.y}px" onclick={(e) => e.stopPropagation()}>
			<button onclick={() => startRename(ellipsisMenu.type, ellipsisMenu.item)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M2 11.5l.5-2L9 3l2 2-6.5 6.5-2 .5z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
				Rename
			</button>
			{#if ellipsisMenu.type === 'doc'}
				<button onclick={() => { openNoteEditor(ellipsisMenu.item); closeMenus(); }}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M2 11.5l.5-2L9 3l2 2-6.5 6.5-2 .5z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
					Edit
				</button>
			{/if}
			{#if ellipsisMenu.type === 'file'}
				<button onclick={() => handleDownload(ellipsisMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M7 2v8M4 7l3 3 3-3M2 12h10" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
					Download
				</button>
				<button onclick={() => handleDuplicate(ellipsisMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><rect x="4" y="4" width="8" height="8" rx="1" stroke="currentColor" stroke-width="1.2"/><path d="M10 4V3a1 1 0 00-1-1H3a1 1 0 00-1 1v6a1 1 0 001 1h1" stroke="currentColor" stroke-width="1.2"/></svg>
					Duplicate
				</button>
				<button onclick={() => handleShare(ellipsisMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><circle cx="10" cy="3" r="1.5" stroke="currentColor" stroke-width="1.2"/><circle cx="4" cy="7" r="1.5" stroke="currentColor" stroke-width="1.2"/><circle cx="10" cy="11" r="1.5" stroke="currentColor" stroke-width="1.2"/><path d="M5.4 6.2l3.2-2.4M5.4 7.8l3.2 2.4" stroke="currentColor" stroke-width="1.2"/></svg>
					Share Link
				</button>
				<div class="menu-divider"></div>
				<button onclick={() => handleAddToKnowledge(ellipsisMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M7 1v6M4 4l3 3 3-3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M2 9a2 2 0 002 2h6a2 2 0 002-2" stroke="currentColor" stroke-width="1.2"/></svg>
					Add to Knowledge Base
				</button>
			{/if}
			<div class="menu-divider"></div>
			<button class="menu-danger" onclick={() => handleDelete(ellipsisMenu.type, ellipsisMenu.item)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M3 4h8M5.5 4V3a.5.5 0 01.5-.5h2a.5.5 0 01.5.5v1M4 4l.5 8h5L10 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
				Delete
			</button>
		</div>
	{/if}

	<!-- Right-click context menu -->
	{#if contextMenu}
		<!-- svelte-ignore a11y_no_static_element_interactions -->
		<div class="dropdown-menu" style="left: {contextMenu.x}px; top: {contextMenu.y}px" onclick={(e) => e.stopPropagation()}>
			<button onclick={() => startRename(contextMenu.type, contextMenu.item)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M2 11.5l.5-2L9 3l2 2-6.5 6.5-2 .5z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
				Rename
			</button>
			{#if contextMenu.type === 'doc'}
				<button onclick={() => { openNoteEditor(contextMenu.item); closeMenus(); }}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M2 11.5l.5-2L9 3l2 2-6.5 6.5-2 .5z" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round"/></svg>
					Edit
				</button>
			{/if}
			{#if contextMenu.type === 'file'}
				<button onclick={() => { handleDownload(contextMenu.item); closeMenus(); }}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M7 2v8M4 7l3 3 3-3M2 12h10" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
					Download
				</button>
				<button onclick={() => handleDuplicate(contextMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><rect x="4" y="4" width="8" height="8" rx="1" stroke="currentColor" stroke-width="1.2"/><path d="M10 4V3a1 1 0 00-1-1H3a1 1 0 00-1 1v6a1 1 0 001 1h1" stroke="currentColor" stroke-width="1.2"/></svg>
					Duplicate
				</button>
				<button onclick={() => handleShare(contextMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><circle cx="10" cy="3" r="1.5" stroke="currentColor" stroke-width="1.2"/><circle cx="4" cy="7" r="1.5" stroke="currentColor" stroke-width="1.2"/><circle cx="10" cy="11" r="1.5" stroke="currentColor" stroke-width="1.2"/><path d="M5.4 6.2l3.2-2.4M5.4 7.8l3.2 2.4" stroke="currentColor" stroke-width="1.2"/></svg>
					Share Link
				</button>
				<div class="menu-divider"></div>
				<button onclick={() => handleAddToKnowledge(contextMenu.item)}>
					<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M7 1v6M4 4l3 3 3-3" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M2 9a2 2 0 002 2h6a2 2 0 002-2" stroke="currentColor" stroke-width="1.2"/></svg>
					Add to Knowledge Base
				</button>
			{/if}
			<div class="menu-divider"></div>
			<button class="menu-danger" onclick={() => handleDelete(contextMenu.type, contextMenu.item)}>
				<svg width="14" height="14" viewBox="0 0 14 14" fill="none"><path d="M3 4h8M5.5 4V3a.5.5 0 01.5-.5h2a.5.5 0 01.5.5v1M4 4l.5 8h5L10 4" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" stroke-linejoin="round"/></svg>
				Delete
			</button>
		</div>
	{/if}
</div>

<style>
	.files-page {
		display: flex;
		flex-direction: column;
		height: 100vh;
		background: var(--bg-root);
		color: var(--text-primary);
		font-family: var(--font-sans);
	}
	.files-header {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: var(--space-md) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		flex-shrink: 0;
	}
	.header-left {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.header-left h1 { font-size: var(--text-xl); font-weight: 700; margin: 0; }
	.back-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 28px;
		height: 28px;
		background: none;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		cursor: pointer;
	}
	.back-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }
	.header-right {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
	}
	.selection-count {
		font-size: var(--text-sm);
		color: var(--accent);
		font-weight: 600;
	}
	.uploader-filter-btn {
		padding: 4px 10px; background: var(--bg-raised); color: var(--text-secondary);
		border: 1px solid var(--border-default); border-radius: var(--radius-md);
		font-size: var(--text-xs); cursor: pointer; font-family: inherit; font-weight: 500;
	}
	.uploader-filter-btn.active { background: var(--accent); color: var(--text-inverse); border-color: var(--accent); }
	.file-uploader-avatar {
		width: 18px; height: 18px; border-radius: 50%; display: inline-flex; align-items: center; justify-content: center;
		font-size: 10px; font-weight: 700; color: #fff; flex-shrink: 0; margin-right: 4px; vertical-align: middle;
	}
	.sort-select {
		background: var(--bg-raised);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		padding: 4px 8px;
		font-family: inherit;
		cursor: pointer;
		outline: none;
	}
	.sort-select:focus { border-color: var(--accent); }

	.view-toggle {
		display: flex;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		overflow: hidden;
	}
	.toggle-btn {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 32px;
		height: 28px;
		background: var(--bg-raised);
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
	}
	.toggle-btn:hover { color: var(--text-secondary); }
	.toggle-btn.active { background: var(--accent); color: var(--text-inverse); }
	.btn-action, .btn-upload {
		padding: 6px 14px;
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 500;
		cursor: pointer;
		font-family: inherit;
		background: var(--bg-raised);
		color: var(--text-primary);
	}
	.btn-action:hover { border-color: var(--border-strong); }
	.btn-upload {
		background: var(--accent);
		color: var(--text-inverse);
		border-color: var(--accent);
	}
	.btn-upload:hover { background: var(--accent-hover); }

	/* Breadcrumbs */
	.breadcrumbs {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		padding: var(--space-sm) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		font-size: var(--text-sm);
		flex-shrink: 0;
	}
	.crumb {
		background: none;
		border: none;
		color: var(--text-secondary);
		cursor: pointer;
		font-size: var(--text-sm);
		font-family: inherit;
		padding: 2px 4px;
		border-radius: var(--radius-sm);
	}
	.crumb:hover { color: var(--text-primary); background: var(--bg-raised); }
	.crumb.active { color: var(--text-primary); font-weight: 600; }
	.crumb-sep { color: var(--text-tertiary); }

	/* New folder */
	.new-folder-bar {
		display: flex;
		gap: var(--space-sm);
		padding: var(--space-sm) var(--space-xl);
		background: var(--bg-surface);
		border-bottom: 1px solid var(--border-subtle);
	}
	.new-folder-bar input {
		flex: 1;
		padding: 6px 10px;
		background: var(--bg-input);
		color: var(--text-primary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-family: inherit;
	}
	.btn-create {
		padding: 6px 14px;
		background: var(--accent);
		color: var(--text-inverse);
		border: none;
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		font-weight: 600;
		cursor: pointer;
		font-family: inherit;
	}
	.btn-cancel {
		padding: 6px 14px;
		background: none;
		color: var(--text-secondary);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		font-size: var(--text-sm);
		cursor: pointer;
		font-family: inherit;
	}

	/* Content area */
	.files-content {
		flex: 1;
		display: flex;
		overflow: hidden;
	}
	.files-area {
		flex: 1;
		overflow-y: auto;
		position: relative;
		transition: background 200ms;
	}
	.files-area.drag-over {
		background: var(--accent-glow);
	}
	.upload-overlay {
		position: absolute;
		inset: 0;
		display: flex;
		align-items: center;
		justify-content: center;
		background: rgba(0,0,0,0.5);
		color: var(--accent);
		font-size: var(--text-lg);
		font-weight: 600;
		z-index: 10;
	}

	/* Grid view */
	.grid {
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
		gap: var(--space-md);
		padding: var(--space-lg);
	}
	.grid-item {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: var(--space-xs);
		padding: var(--space-md);
		border: 1px solid var(--border-subtle);
		border-radius: var(--radius-md);
		cursor: pointer;
		transition: border-color 150ms, background 150ms;
		text-align: center;
		position: relative;
	}
	.grid-item:hover { border-color: var(--border-strong); background: var(--bg-surface); }
	.grid-item.selected { border-color: var(--accent); background: var(--accent-glow); }

	/* Checkbox on items */
	.item-checkbox {
		position: absolute;
		top: 6px;
		left: 6px;
		opacity: 0;
		transition: opacity 150ms;
		z-index: 2;
	}
	.grid-item:hover .item-checkbox,
	.grid-item.selected .item-checkbox { opacity: 1; }
	.item-checkbox input {
		width: 14px;
		height: 14px;
		accent-color: var(--accent);
		cursor: pointer;
	}

	/* Ellipsis button on grid items */
	.ellipsis-btn {
		position: absolute;
		top: 6px;
		right: 6px;
		width: 24px;
		height: 24px;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--bg-raised);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-sm);
		color: var(--text-tertiary);
		cursor: pointer;
		opacity: 0;
		transition: opacity 150ms;
		z-index: 2;
	}
	.grid-item:hover .ellipsis-btn { opacity: 1; }
	.ellipsis-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }

	/* Ellipsis button inline (list view) */
	.ellipsis-btn-inline {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 24px;
		height: 24px;
		background: none;
		border: none;
		color: var(--text-tertiary);
		cursor: pointer;
		border-radius: var(--radius-sm);
		opacity: 0;
		transition: opacity 150ms;
	}
	.list-row:hover .ellipsis-btn-inline { opacity: 1; }
	.ellipsis-btn-inline:hover { color: var(--text-primary); background: var(--bg-raised); }

	.grid-icon { font-size: 32px; margin-bottom: var(--space-xs); }
	.folder-icon { opacity: 0.9; }
	.doc-icon { opacity: 0.9; }
	.grid-thumbnail {
		width: 100%;
		height: 80px;
		overflow: hidden;
		border-radius: var(--radius-sm);
		margin-bottom: var(--space-xs);
	}
	.grid-thumbnail img {
		width: 100%;
		height: 100%;
		object-fit: cover;
	}
	.grid-name {
		font-size: var(--text-xs);
		font-weight: 500;
		color: var(--text-primary);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		max-width: 100%;
	}
	.grid-size {
		font-size: 10px;
		color: var(--text-tertiary);
	}
	.private-badge {
		font-size: 9px;
		padding: 0 4px;
		background: rgba(239,68,68,0.15);
		color: var(--red);
		border-radius: var(--radius-full);
	}

	/* List view */
	.list-view { width: 100%; }
	.list-header {
		display: flex;
		padding: var(--space-sm) var(--space-xl);
		font-size: var(--text-xs);
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.06em;
		color: var(--text-tertiary);
		border-bottom: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		position: sticky;
		top: 0;
	}
	.list-row {
		display: flex;
		padding: var(--space-sm) var(--space-xl);
		border-bottom: 1px solid var(--border-subtle);
		cursor: pointer;
		transition: background 150ms;
		align-items: center;
	}
	.list-row:hover { background: var(--bg-surface); }
	.list-row.selected { background: var(--accent-glow); }
	.list-check-col {
		width: 28px;
		flex-shrink: 0;
		display: flex;
		align-items: center;
	}
	.list-check-col input {
		width: 14px;
		height: 14px;
		accent-color: var(--accent);
		cursor: pointer;
	}
	.list-name-col {
		flex: 1;
		min-width: 0;
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		font-size: var(--text-sm);
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.list-icon { flex-shrink: 0; }
	.list-size-col { width: 80px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); }
	.list-type-col { width: 100px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); }
	.list-date-col { width: 100px; flex-shrink: 0; font-size: var(--text-xs); color: var(--text-tertiary); }
	.list-actions-col { width: 32px; flex-shrink: 0; display: flex; justify-content: center; }

	/* Rename */
	.rename-input, .rename-input-inline {
		padding: 4px 8px;
		background: var(--bg-input);
		color: var(--text-primary);
		border: 1px solid var(--accent);
		border-radius: var(--radius-sm);
		font-size: var(--text-xs);
		font-family: inherit;
		width: 100%;
	}
	.rename-input:focus, .rename-input-inline:focus { outline: none; }
	.rename-input-inline { width: auto; flex: 1; }

	/* Note editor panel */
	.note-editor-panel {
		flex-shrink: 0;
		display: flex;
		flex-direction: column;
		border-left: 1px solid var(--border-subtle);
		background: var(--bg-surface);
		overflow: hidden;
		position: relative;
	}
	.note-editor-panel.resizing { user-select: none; }
	.resize-handle {
		position: absolute;
		left: 0;
		top: 0;
		bottom: 0;
		width: 4px;
		cursor: col-resize;
		z-index: 10;
	}
	.resize-handle:hover,
	.note-editor-panel.resizing .resize-handle {
		background: var(--accent);
		opacity: 0.4;
	}
	.note-editor-header {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		padding: var(--space-md);
		border-bottom: 1px solid var(--border-subtle);
	}
	.note-title-input {
		flex: 1;
		background: none;
		border: none;
		font-size: var(--text-lg);
		font-weight: 700;
		color: var(--text-primary);
		padding: 0;
		font-family: inherit;
		outline: none;
	}
	.note-title-input::placeholder { color: var(--text-tertiary); }
	.note-editor-actions {
		display: flex;
		align-items: center;
		gap: var(--space-xs);
		flex-shrink: 0;
	}
	.note-saving {
		font-size: var(--text-xs);
		color: var(--accent);
		opacity: 0;
		transition: opacity 0.2s;
	}
	.note-saving.visible { opacity: 1; }
	.toolbar-btn {
		padding: 4px 10px;
		background: var(--bg-raised);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		color: var(--text-secondary);
		font-size: var(--text-xs);
		font-family: inherit;
		cursor: pointer;
	}
	.toolbar-btn:hover { color: var(--text-primary); border-color: var(--border-strong); }
	.toolbar-btn.danger:hover { color: var(--red); border-color: var(--red); }
	.toolbar-btn.md-active {
		background: var(--accent-glow);
		color: var(--accent);
		border-color: var(--accent-border);
	}
	.note-editor-body {
		flex: 1;
		overflow-y: auto;
		padding: var(--space-md);
	}

	/* Dropdown menu (ellipsis + context) */
	.dropdown-menu {
		position: fixed;
		background: var(--bg-raised);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		padding: var(--space-xs);
		z-index: 100;
		box-shadow: var(--shadow-md);
		display: flex;
		flex-direction: column;
		min-width: 180px;
	}
	.dropdown-menu button {
		display: flex;
		align-items: center;
		gap: var(--space-sm);
		width: 100%;
		padding: 7px 12px;
		background: none;
		border: none;
		color: var(--text-primary);
		font-size: var(--text-sm);
		font-family: inherit;
		cursor: pointer;
		text-align: left;
		border-radius: var(--radius-sm);
	}
	.dropdown-menu button:hover { background: var(--bg-surface); }
	.dropdown-menu button svg { flex-shrink: 0; opacity: 0.6; }
	.menu-divider {
		height: 1px;
		background: var(--border-subtle);
		margin: var(--space-xs) 0;
	}
	.menu-danger { color: var(--red) !important; }
	.menu-danger:hover { background: rgba(239,68,68,0.1) !important; }
	.menu-danger svg { opacity: 0.8 !important; }

	.empty-state {
		grid-column: 1 / -1;
		text-align: center;
		padding: 3rem;
		color: var(--text-tertiary);
		font-size: var(--text-sm);
	}

	/* Drop target highlight */
	.drop-highlight {
		outline: 2px solid var(--accent) !important;
		background: var(--accent-glow) !important;
	}
	.crumb.drop-highlight {
		background: var(--accent-glow) !important;
		outline: 2px solid var(--accent);
		border-radius: var(--radius-sm);
	}

	/* Move to dropdown */
	.move-to-wrapper {
		position: relative;
	}
	.move-dropdown {
		position: absolute;
		top: 100%;
		left: 0;
		margin-top: 4px;
		background: var(--bg-raised);
		border: 1px solid var(--border-default);
		border-radius: var(--radius-md);
		padding: var(--space-xs);
		z-index: 100;
		box-shadow: var(--shadow-md);
		display: flex;
		flex-direction: column;
		min-width: 180px;
		max-height: 240px;
		overflow-y: auto;
	}
	.move-dropdown button {
		display: flex;
		align-items: center;
		width: 100%;
		padding: 7px 12px;
		background: none;
		border: none;
		color: var(--text-primary);
		font-size: var(--text-sm);
		font-family: inherit;
		cursor: pointer;
		text-align: left;
		border-radius: var(--radius-sm);
	}
	.move-dropdown button:hover { background: var(--bg-surface); }
</style>
