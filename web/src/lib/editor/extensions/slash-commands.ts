import { Extension } from '@tiptap/core';
import Suggestion from '@tiptap/suggestion';
import tippy, { type Instance as TippyInstance } from 'tippy.js';

interface CommandItem {
  title: string;
  icon: string;
  command: (props: { editor: any; range: any }) => void;
}

const COMMANDS: CommandItem[] = [
  {
    title: 'Heading 1',
    icon: 'H1',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).setNode('heading', { level: 1 }).run();
    },
  },
  {
    title: 'Heading 2',
    icon: 'H2',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).setNode('heading', { level: 2 }).run();
    },
  },
  {
    title: 'Heading 3',
    icon: 'H3',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).setNode('heading', { level: 3 }).run();
    },
  },
  {
    title: 'Bullet List',
    icon: '•',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).toggleBulletList().run();
    },
  },
  {
    title: 'Ordered List',
    icon: '1.',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).toggleOrderedList().run();
    },
  },
  {
    title: 'Checklist',
    icon: '☑',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).toggleTaskList().run();
    },
  },
  {
    title: 'Code Block',
    icon: '<>',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).toggleCodeBlock().run();
    },
  },
  {
    title: 'Blockquote',
    icon: '❝',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).toggleBlockquote().run();
    },
  },
  {
    title: 'Horizontal Rule',
    icon: '—',
    command: ({ editor, range }) => {
      editor.chain().focus().deleteRange(range).setHorizontalRule().run();
    },
  },
  {
    title: 'Image',
    icon: '🖼',
    command: ({ editor, range }) => {
      const url = prompt('Image URL:');
      if (url) {
        editor.chain().focus().deleteRange(range).setImage({ src: url }).run();
      }
    },
  },
];

function createSuggestionRenderer() {
  let popup: TippyInstance | null = null;
  let menuEl: HTMLElement | null = null;
  let selectedIndex = 0;
  let filteredItems: CommandItem[] = [];

  function render() {
    if (!menuEl) return;
    menuEl.innerHTML = '';
    filteredItems.forEach((item, index) => {
      const btn = document.createElement('button');
      btn.className = `slash-menu-item${index === selectedIndex ? ' is-selected' : ''}`;
      btn.innerHTML = `<span class="icon">${item.icon}</span><span class="label">${item.title}</span>`;
      btn.addEventListener('mouseenter', () => {
        selectedIndex = index;
        render();
      });
      btn.addEventListener('click', () => {
        selectItem(index);
      });
      menuEl!.appendChild(btn);
    });
  }

  let commandProps: any = null;

  function selectItem(index: number) {
    const item = filteredItems[index];
    if (item && commandProps) {
      item.command({ editor: commandProps.editor, range: commandProps.range });
    }
  }

  return {
    onStart(props: any) {
      commandProps = props;
      filteredItems = COMMANDS;
      selectedIndex = 0;

      menuEl = document.createElement('div');
      menuEl.className = 'slash-menu';

      popup = tippy(document.body, {
        getReferenceClientRect: props.clientRect,
        appendTo: () => document.body,
        content: menuEl,
        showOnCreate: true,
        interactive: true,
        trigger: 'manual',
        placement: 'bottom-start',
        arrow: false,
        offset: [0, 4],
      });

      render();
    },

    onUpdate(props: any) {
      commandProps = props;
      const query = (props.query || '').toLowerCase();
      filteredItems = COMMANDS.filter((item) =>
        item.title.toLowerCase().includes(query)
      );
      selectedIndex = 0;

      if (popup) {
        popup.setProps({ getReferenceClientRect: props.clientRect });
      }

      render();

      if (filteredItems.length === 0 && popup) {
        popup.hide();
      } else if (popup) {
        popup.show();
      }
    },

    onKeyDown(props: any) {
      const { event } = props;

      if (event.key === 'ArrowUp') {
        selectedIndex = (selectedIndex - 1 + filteredItems.length) % filteredItems.length;
        render();
        return true;
      }

      if (event.key === 'ArrowDown') {
        selectedIndex = (selectedIndex + 1) % filteredItems.length;
        render();
        return true;
      }

      if (event.key === 'Enter') {
        selectItem(selectedIndex);
        return true;
      }

      if (event.key === 'Escape') {
        if (popup) popup.hide();
        return true;
      }

      return false;
    },

    onExit() {
      if (popup) {
        popup.destroy();
        popup = null;
      }
      menuEl = null;
      commandProps = null;
    },
  };
}

export const SlashCommands = Extension.create({
  name: 'slashCommands',

  addOptions() {
    return {
      suggestion: {
        char: '/',
        startOfLine: false,
        command: ({ editor, range, props }: any) => {
          props.command({ editor, range });
        },
        items: ({ query }: { query: string }) => {
          return COMMANDS.filter((item) =>
            item.title.toLowerCase().includes(query.toLowerCase())
          );
        },
        render: createSuggestionRenderer,
      },
    };
  },

  addProseMirrorPlugins() {
    return [
      Suggestion({
        editor: this.editor,
        ...this.options.suggestion,
      }),
    ];
  },
});
