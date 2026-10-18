"use client";

import { useRef, useState } from "react";
import { contentReferencesAttachment } from "@multica/core/types";
import { useCommentDraftStore, type CommentDraftKey } from "@multica/core/issues/stores";
import { Button } from "@multica/ui/components/ui/button";
import { ContentEditor, useUploadGate, type ContentEditorRef } from "../editor";
import { useCommentUploads } from "../issues/components/use-comment-uploads";
import { useT } from "../i18n";

export function SpecCommentComposer({ specId, versionId, onSend }: { specId: string; versionId: string; onSend: (content: string, attachmentIds: string[]) => Promise<void> }) {
  const { t } = useT("projects");
  const key: CommentDraftKey = `new:spec:${specId}:${versionId}`;
  const editorRef = useRef<ContentEditorRef>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const [initial] = useState(() => useCommentDraftStore.getState().getDraft(key) ?? "");
  const [content, setContent] = useState(initial);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState(false);
  const sendingRef = useRef(false);
  const editorGate = useUploadGate(editorRef);
  const { attachments, handleUpload, gate } = useCommentUploads(key, {}, editorGate, editorRef);

  async function submit() {
    if (sendingRef.current || gate.isBlocked()) return;
    const body = editorRef.current?.flushPendingUpdate() ?? content;
    if (!body.trim()) return;
    const store = useCommentDraftStore.getState();
    store.setDraft(key, body);
    const snapshot = useCommentDraftStore.getState().drafts[key];
    const ids = attachments.filter(a => contentReferencesAttachment(body, a)).map(a => a.id);
    sendingRef.current = true;
    setSending(true);
    setError(false);
    try {
      await onSend(body, ids);
      const latestBody = editorRef.current?.flushPendingUpdate();
      if (latestBody != null) useCommentDraftStore.getState().setDraft(key, latestBody);
      if (useCommentDraftStore.getState().drafts[key] === snapshot) {
        useCommentDraftStore.getState().clearDraft(key);
        editorRef.current?.clearContent();
        setContent("");
      }
    } catch {
      setError(true);
    } finally {
      sendingRef.current = false;
      setSending(false);
    }
  }
  return <form onSubmit={event => { event.preventDefault(); void submit(); }} className="space-y-2">
    <div role="group" aria-label={t($ => $.specs.comment_label)} className={sending ? "pointer-events-none opacity-60" : ""} aria-busy={sending || undefined}>
      <ContentEditor ref={editorRef} defaultValue={initial} placeholder={t($ => $.specs.comment_label)} onUpdate={value => { setContent(value); useCommentDraftStore.getState().setDraft(key, value); }} onUploadFile={handleUpload} onUploadingChange={editorGate.onUploadingChange} attachments={attachments} debounceMs={0} />
    </div>
    <input ref={fileRef} type="file" className="hidden" onChange={event => { const file = event.target.files?.[0]; if (file) editorRef.current?.uploadFile(file); event.target.value = ""; }} />
    <Button type="button" size="sm" variant="outline" disabled={sending} onClick={() => fileRef.current?.click()}>{t($ => $.specs.attach_file)}</Button>{" "}
    <Button type="submit" size="sm" disabled={sending || gate.uploading || !content.trim()}>{t($ => $.specs.send)}</Button>
    {error && <p role="alert">{t($ => $.specs.action_error)}</p>}
  </form>;
}
