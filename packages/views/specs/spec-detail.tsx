"use client";

import { useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { useAuthStore } from "@multica/core/auth";
import { useConfigStore } from "@multica/core/config";
import { useWorkspacePaths } from "@multica/core/paths";
import { useActorName } from "@multica/core/workspace/hooks";
import { specCommentsOptions, specDetailOptions, specIssuesOptions, specVersionsOptions, useSpecActions } from "@multica/core/specs";
import { Button } from "@multica/ui/components/ui/button";
import { ReactionBar } from "@multica/ui/components/common/reaction-bar";
import { AlertDialog, AlertDialogContent, AlertDialogHeader, AlertDialogTitle, AlertDialogDescription, AlertDialogFooter, AlertDialogCancel } from "@multica/ui/components/ui/alert-dialog";
import { ContentEditor, ReadonlyContent, type ContentEditorRef } from "../editor";
import { useT } from "../i18n";
import { AppLink } from "../navigation";
import { orderSpecComments } from "./comment-order";

export function SpecDetail({ id }: { id: string }) {
  const { t } = useT("projects");
  const { t: tIssues } = useT("issues");
  const wsId = useWorkspaceId();
  const userId = useAuthStore(s => s.user?.id);
  const keepReplies = useConfigStore(s => s.commentDeleteKeepRepliesSupported);
  const paths = useWorkspacePaths();
  const { getActorName } = useActorName();
  const detail = useQuery(specDetailOptions(wsId, id));
  const versions = useQuery(specVersionsOptions(wsId, id));
  const issues = useQuery(specIssuesOptions(wsId, id));
  const actions = useSpecActions(id);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [confirmDeprecated, setConfirmDeprecated] = useState(false);
  const [content, setContent] = useState("");
  const [changelog, setChangelog] = useState("");
  const changelogRef = useRef("");
  const editorRef = useRef<ContentEditorRef>(null);
  const [comment, setComment] = useState("");
  const [replyTo, setReplyTo] = useState<string | undefined>();
  const [editingComment, setEditingComment] = useState<string | null>(null);
  const [commentEdit, setCommentEdit] = useState("");
  const [deleteCommentId, setDeleteCommentId] = useState<string | null>(null);
  const [error, setError] = useState(false);
  const selected = versions.data?.versions.find((v) => v.id === selectedId) ?? versions.data?.versions.find((v) => v.id === detail.data?.current_version_id);
  const comments = useQuery(specCommentsOptions(wsId, id, selected?.id ?? ""));
  if (detail.isLoading) return <p>{t($ => $.specs.loading)}</p>;
  if (!detail.data) return <p role="alert">{t($ => $.specs.load_detail_error)}</p>;
  const spec = detail.data;
  const status = spec.status === "draft" ? t($ => $.specs.status_draft) : spec.status === "in_review" ? t($ => $.specs.status_in_review) : spec.status === "approved" ? t($ => $.specs.status_approved) : spec.status === "deprecated" ? t($ => $.specs.status_deprecated) : spec.status;
  const run = async (action: () => Promise<unknown>) => { setError(false); try { await action(); } catch { setError(true); } };
  return <main className="min-h-0 flex-1 overflow-auto p-6"><div className="mx-auto max-w-4xl space-y-6">
    <header><span className="text-caption text-muted-foreground">{spec.identifier}</span><h1 className="text-title-lg font-semibold">{spec.title}</h1><p>{status}</p></header>
    <div className="flex flex-wrap gap-2">
      {spec.status !== "deprecated" && <Button size="sm" onClick={() => { setContent(spec.current_version?.content ?? ""); setChangelog(""); changelogRef.current = ""; setEditing(true); }}>{t($ => $.specs.publish)}</Button>}
      {spec.status === "draft" && <Button size="sm" variant="outline" disabled={!spec.current_version_id || actions.transition.isPending} onClick={() => void run(() => actions.transition.mutateAsync("in_review"))}>{t($ => $.specs.submit_review)}</Button>}
      {spec.status === "in_review" && <Button size="sm" variant="outline" disabled={actions.transition.isPending} onClick={() => void run(() => actions.transition.mutateAsync("approved"))}>{t($ => $.specs.approve)}</Button>}
      {["draft", "in_review", "approved"].includes(spec.status) && <Button size="sm" variant="outline" disabled={actions.transition.isPending} onClick={() => setConfirmDeprecated(true)}>{t($ => $.specs.deprecate)}</Button>}
    </div>
    <AlertDialog open={confirmDeprecated} onOpenChange={setConfirmDeprecated}>
      <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t($ => $.specs.deprecate)}</AlertDialogTitle><AlertDialogDescription>{t($ => $.specs.deprecate_confirm)}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel>{t($ => $.specs.cancel)}</AlertDialogCancel><Button variant="destructive" disabled={actions.transition.isPending} onClick={() => void run(async () => { await actions.transition.mutateAsync("deprecated"); setConfirmDeprecated(false); })}>{t($ => $.specs.deprecate)}</Button></AlertDialogFooter>{error && <p role="alert">{t($ => $.specs.action_error)}</p>}</AlertDialogContent>
    </AlertDialog>
    <AlertDialog open={!!deleteCommentId} onOpenChange={open => { if (!open && !actions.deleteComment.isPending) setDeleteCommentId(null); }}>
      <AlertDialogContent><AlertDialogHeader><AlertDialogTitle>{t($ => $.specs.delete_comment)}</AlertDialogTitle><AlertDialogDescription>{keepReplies ? t($ => $.specs.delete_comment_confirm) : t($ => $.specs.delete_comment_with_replies_confirm)}</AlertDialogDescription></AlertDialogHeader><AlertDialogFooter><AlertDialogCancel disabled={actions.deleteComment.isPending}>{t($ => $.specs.cancel)}</AlertDialogCancel><Button variant="destructive" disabled={actions.deleteComment.isPending} onClick={() => { if (deleteCommentId) void run(async () => { await actions.deleteComment.mutateAsync({ id: deleteCommentId, keepReplies }); setDeleteCommentId(null); }); }}>{t($ => $.specs.delete)}</Button></AlertDialogFooter>{error && <p role="alert">{t($ => $.specs.action_error)}</p>}</AlertDialogContent>
    </AlertDialog>
    {error && <p role="alert">{t($ => $.specs.action_error)}</p>}
    {editing && <form className="space-y-3" onSubmit={event => { event.preventDefault(); const submittedContent = editorRef.current?.getMarkdown() ?? content; const submittedChangelog = changelog;
      void run(async () => { await actions.publish.mutateAsync({ content: submittedContent, changelog: submittedChangelog }); if ((editorRef.current?.getMarkdown() ?? content) === submittedContent && changelogRef.current === submittedChangelog) setEditing(false); setSelectedId(null); }); }}>
      <label className="block font-medium">{t($ => $.specs.content)}</label><ContentEditor ref={editorRef} defaultValue={content} onUpdate={setContent} debounceMs={0} />
      <label className="block">{t($ => $.specs.changelog)}<input className="mt-1 block w-full rounded border bg-background px-2 py-1" value={changelog} onChange={event => { changelogRef.current = event.target.value; setChangelog(event.target.value); }} /></label>
      <div className="flex gap-2"><Button type="submit" size="sm" disabled={!content.trim() || !changelog.trim() || actions.publish.isPending}>{t($ => $.specs.publish)}</Button><Button type="button" variant="outline" size="sm" onClick={() => setEditing(false)}>{t($ => $.specs.cancel)}</Button></div>
    </form>}
    <section className="space-y-2"><h2 className="font-medium">{t($ => $.specs.versions)}</h2>
      {versions.isError && <p role="alert">{t($ => $.specs.load_error)}</p>}
      {!versions.isLoading && !versions.isError && !versions.data?.versions.length && <p>{t($ => $.specs.no_versions)}</p>}
      {versions.data?.versions.map(version => <button type="button" key={version.id} onClick={() => { setSelectedId(version.id); setReplyTo(undefined); }} className={`block w-full rounded p-2 text-left hover:bg-accent ${version.id === selected?.id ? "bg-accent" : ""}`} aria-pressed={version.id === selected?.id}>
        <span>{t($ => $.specs.version_number, { number: version.number })}</span>{" · "}<span>{version.changelog}</span>{" · "}<span>{version.created_by ? getActorName("member", version.created_by) : ""}</span>{" · "}<time dateTime={version.created_at}>{new Date(version.created_at).toLocaleString()}</time>
      </button>)}
      {selected && <ReadonlyContent content={selected.content} />}
    </section>
    <section><h2 className="font-medium">{t($ => $.specs.related_issues)}</h2>{issues.isError && <p role="alert">{t($ => $.specs.load_error)}</p>}{issues.data?.issues.map(issue => <AppLink key={issue.id} href={paths.issueDetail(issue.id)} className="block hover:underline">{issue.identifier} {issue.title}</AppLink>)}</section>
    {selected && <section className="space-y-3"><h2 className="font-medium">{t($ => $.specs.comments)}</h2>
      {comments.isError && <p role="alert">{t($ => $.specs.load_error)}</p>}
      {orderSpecComments(comments.data ?? []).map(({ comment: item, depth }) => <div key={item.id} className={depth ? "ml-6 border-l pl-3" : "border-t pt-3"}><div className="text-caption text-muted-foreground">{getActorName(item.author_type, item.author_id)}</div>{item.deleted_at ? <p className="text-muted-foreground">{tIssues($ => $.comment.deleted_placeholder)}</p> : editingComment === item.id ? <form onSubmit={event => { event.preventDefault(); void run(async () => { await actions.editComment.mutateAsync({ id: item.id, content: commentEdit, attachmentIds: item.attachments.map(a => a.id), revision: item.revision }); setEditingComment(null); }); }}><label>{t($ => $.specs.edit_comment)}<textarea className="block w-full rounded border bg-background p-2" value={commentEdit} disabled={actions.editComment.isPending} onChange={event => setCommentEdit(event.target.value)} /></label><Button type="submit" size="xs" disabled={!commentEdit.trim() || actions.editComment.isPending}>{t($ => $.specs.save)}</Button><Button type="button" size="xs" variant="ghost" onClick={() => setEditingComment(null)}>{t($ => $.specs.cancel)}</Button></form> : <ReadonlyContent content={item.content} attachments={item.attachments} />}{!item.deleted_at && <ReactionBar reactions={item.reactions} currentUserId={userId} getActorName={getActorName} onToggle={emoji => { if (!actions.reaction.isPending) void run(() => actions.reaction.mutateAsync({ id: item.id, emoji, reacted: item.reactions.some(reaction => reaction.emoji === emoji && reaction.actor_type === "member" && reaction.actor_id === userId) })); }} />}{!item.deleted_at && <Button size="xs" variant="ghost" onClick={() => setReplyTo(item.id)}>{t($ => $.specs.reply)}</Button>}{!item.deleted_at && <Button size="xs" variant="ghost" disabled={actions.resolveComment.isPending} onClick={() => void run(() => actions.resolveComment.mutateAsync({ id: item.id, resolved: !!item.resolved_at }))}>{item.resolved_at ? t($ => $.specs.reopen_comment) : t($ => $.specs.resolve_comment)}</Button>}{item.author_type === "member" && item.author_id === userId && !item.deleted_at && editingComment !== item.id && <Button size="xs" variant="ghost" onClick={() => { setEditingComment(item.id); setCommentEdit(item.content); }}>{t($ => $.specs.edit_comment)}</Button>}{item.author_type === "member" && item.author_id === userId && !item.deleted_at && <Button size="xs" variant="ghost" onClick={() => { setError(false); setDeleteCommentId(item.id); }}>{t($ => $.specs.delete_comment)}</Button>}</div>)}
      <form onSubmit={event => { event.preventDefault(); void run(async () => { await actions.comment.mutateAsync({ versionId: selected.id, content: comment, parentId: replyTo }); setComment(""); setReplyTo(undefined); }); }}>
        {replyTo && <Button type="button" size="xs" variant="ghost" onClick={() => setReplyTo(undefined)}>{t($ => $.specs.cancel)}</Button>}
        <label className="block">{t($ => $.specs.comment_label)}<textarea className="mt-1 block w-full rounded border bg-background p-2" value={comment} disabled={actions.comment.isPending} onChange={event => setComment(event.target.value)} /></label>
        <Button type="submit" size="sm" disabled={!comment.trim() || actions.comment.isPending}>{t($ => $.specs.send)}</Button>
      </form>
    </section>}
  </div></main>;
}
