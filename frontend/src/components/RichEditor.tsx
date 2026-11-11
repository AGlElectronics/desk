import { forwardRef, useImperativeHandle } from 'react';
import { useCreateBlockNote } from '@blocknote/react';
import { BlockNoteView } from '@blocknote/mantine';
import '@blocknote/mantine/style.css';
import { useThemePreference } from '@/lib/theme';

/** What the composer needs from the editor. */
export type RichEditorHandle = {
  /** Current content as HTML (the server sanitises it and derives plain text). */
  getHTML: () => string;
  /** Replaces the content. */
  setHTML: (html: string) => void;
  /** Adds HTML at the end (saved replies). */
  appendHTML: (html: string) => void;
  clear: () => void;
  focus: () => void;
};

type Props = {
  initialHTML?: string;
  placeholder?: string;
  /** Called on every change with whether the editor is empty. */
  onChange?: (empty: boolean) => void;
  ariaLabel?: string;
};

/**
 * BlockNote rich text (docs/adr/0007). Replies are sent as HTML with a
 * plain-text alternative made by the server. Images and files are not
 * offered: attachments are not part of replies yet.
 */
const RichEditor = forwardRef<RichEditorHandle, Props>(function RichEditor({ initialHTML, placeholder, onChange, ariaLabel }, ref) {
  const editor = useCreateBlockNote({
    initialContent: undefined,
    placeholders: placeholder ? { default: placeholder, emptyDocument: placeholder } : undefined,
  });
  const theme = useThemePreference().resolved;

  useImperativeHandle(
    ref,
    () => ({
      getHTML: () => (editor.isEmpty ? '' : editor.blocksToHTMLLossy(editor.document)),
      setHTML: (html: string) => editor.replaceBlocks(editor.document, editor.tryParseHTMLToBlocks(html)),
      appendHTML: (html: string) => {
        const blocks = editor.tryParseHTMLToBlocks(html);
        if (editor.isEmpty) editor.replaceBlocks(editor.document, blocks);
        else editor.insertBlocks(blocks, editor.document[editor.document.length - 1], 'after');
      },
      clear: () => editor.replaceBlocks(editor.document, [{ type: 'paragraph' }]),
      focus: () => editor.focus(),
    }),
    [editor]
  );

  return (
    <div
      aria-label={ariaLabel}
      className="rounded-md border border-input bg-background min-h-[8rem] py-2 [&_.bn-editor]:px-3"
      ref={(el) => {
        if (el && initialHTML && editor.isEmpty) {
          editor.replaceBlocks(editor.document, editor.tryParseHTMLToBlocks(initialHTML));
        }
      }}
    >
      <BlockNoteView
        editor={editor}
        theme={theme === 'dark' ? 'dark' : 'light'}
        sideMenu={false}
        filePanel={false}
        onChange={() => onChange?.(editor.isEmpty)}
      />
    </div>
  );
});

export default RichEditor;
