"use client";

import { useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { isStructuredHandoffDraft } from "../lib/agent-handoff";
import type { AgentHandoffDestination, AgentHandoffDialog as AgentHandoffDialogState } from "./use-agent-handoff";
import { useLocale, useT } from "../../i18n";

type AgentHandoffDialogProps = {
  dialog: AgentHandoffDialogState;
  onDraftChange: (draft: string) => void;
  onDestinationChange: (destination: AgentHandoffDestination) => void;
  onCancel: () => void;
  onSend: (draft: string, destination: AgentHandoffDestination) => Promise<void> | void;
};

function formatSessionTime(value: string, locale: string) {
  return new Intl.DateTimeFormat(locale, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

export function AgentHandoffDialog({
  dialog,
  onDraftChange,
  onDestinationChange,
  onCancel,
  onSend,
}: AgentHandoffDialogProps) {
  const { t } = useT("chat");
  const locale = useLocale();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submissionError, setSubmissionError] = useState<string | null>(null);
  const error = submissionError ?? dialog.error;
  const isBlank = !dialog.draft.trim();
  const isTargetUnavailable = !dialog.targetAvailable;

  const send = async () => {
    if (isBlank || isSubmitting) return;
    setSubmissionError(null);
    setIsSubmitting(true);
    try {
      await onSend(dialog.draft, dialog.destination);
    } catch (cause) {
      setSubmissionError(cause instanceof Error ? cause.message : "delivery_failed");
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => { if (!open && !isSubmitting) onCancel(); }}>
      <DialogContent className="sm:max-w-lg" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{t(($) => $.handoff.title, { name: dialog.target.name })}</DialogTitle>
          <DialogDescription>{t(($) => $.handoff.description, { name: dialog.target.name })}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-2">
          <label className="text-label font-medium" htmlFor="agent-handoff-draft">
            {t(($) => $.handoff.draft_label)}
          </label>
          <Textarea
            id="agent-handoff-draft"
            value={dialog.draft}
            onChange={(event) => onDraftChange(event.target.value)}
            disabled={isSubmitting}
            rows={8}
          />
          {!isStructuredHandoffDraft(dialog.draft) && (
            <p className="text-caption text-muted-foreground">
              {t(($) => $.handoff.incomplete_warning)}
            </p>
          )}
        </div>

        <fieldset className="grid gap-2" disabled={isSubmitting || isTargetUnavailable}>
          <legend className="text-label font-medium">
            {t(($) => $.handoff.receiving_conversation, { name: dialog.target.name })}
          </legend>
          <label className="flex cursor-pointer items-start gap-2 rounded-lg border border-input p-3 has-[:checked]:border-primary has-[:checked]:bg-primary/5">
            <input
              type="radio"
              name="agent-handoff-destination"
              checked={dialog.destination.kind === "new"}
              onChange={() => onDestinationChange({ kind: "new" })}
            />
            <span>{t(($) => $.handoff.new_conversation)}</span>
          </label>
          {dialog.availableSessions.map((session) => {
            const busy = dialog.busySessionIds.has(session.id);
            return (
              <label
                className="flex items-start gap-2 rounded-lg border border-input p-3 has-[:checked]:border-primary has-[:checked]:bg-primary/5 has-[:disabled]:cursor-not-allowed has-[:disabled]:opacity-50"
                key={session.id}
              >
                <input
                  type="radio"
                  name="agent-handoff-destination"
                  checked={dialog.destination.kind === "existing" && dialog.destination.sessionId === session.id}
                  disabled={busy}
                  onChange={() => onDestinationChange({ kind: "existing", sessionId: session.id })}
                />
                <span className="grid gap-0.5">
                  <span>{session.title}</span>
                  <time className="text-caption text-muted-foreground" dateTime={session.updated_at}>
                    {formatSessionTime(session.updated_at, locale)}
                  </time>
                  {busy && <span className="text-caption text-muted-foreground">{t(($) => $.handoff.busy_explanation)}</span>}
                </span>
              </label>
            );
          })}
        </fieldset>

        {isTargetUnavailable && (
          <p className="text-caption text-destructive">
            {t(($) => $.handoff.target_unavailable_explanation, { name: dialog.target.name })}
          </p>
        )}

        {error && (
          <p role="alert" className="text-caption text-destructive">
            {error === "delivery_ambiguous"
              ? t(($) => $.handoff.delivery_ambiguous, {
                name: dialog.target.name,
                conversation: dialog.deliverySession?.title ?? t(($) => $.handoff.selected_conversation),
                id: dialog.deliverySession?.id ?? t(($) => $.handoff.unknown_conversation_id),
              })
              : error === "destination_unavailable"
                ? t(($) => $.handoff.unavailable_target)
                : error === "destination_busy"
                  ? t(($) => $.handoff.busy_explanation)
                  : error === "blank"
                    ? t(($) => $.handoff.blank)
                    : t(($) => $.handoff.failed)}
          </p>
        )}

        <DialogFooter>
          <Button type="button" variant="outline" onClick={onCancel} disabled={isSubmitting}>
            {t(($) => $.handoff.cancel)}
          </Button>
          <Button type="button" onClick={send} disabled={isBlank || isSubmitting || isTargetUnavailable} aria-busy={isSubmitting}>
            {isSubmitting
              ? t(($) => $.handoff.sending, { name: dialog.target.name })
              : t(($) => $.handoff.send, { name: dialog.target.name })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
