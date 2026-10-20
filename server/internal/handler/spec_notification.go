package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec approval notifications (DAWE-7).
//
// notifySpecApproval fans the approval out to the spec's subscribers plus the
// subscribers of every issue linked to the spec. Recipients are members only:
// the inbox is a member surface and an agent subscription (possible only on
// the issue side) is skipped, exactly like the issue-notification fan-out in
// cmd/server/notification_listeners.go. The actor who approved or published
// is never notified about their own action. The notification type is not in
// the preference-group map, so it is always delivered — subscription itself
// is the opt-in.

// notifySpecApproval delivers one spec_approved inbox item per recipient.
// version is the snapshot that took effect (the latest one at approval time,
// or the one just published onto an already-approved spec).
func (h *Handler) notifySpecApproval(r *http.Request, spec db.Spec, version db.SpecVersion, actorType, actorID string) {
	ctx := r.Context()

	recipients := map[string]pgtype.UUID{}
	if rows, err := h.Queries.ListSpecSubscribers(ctx, spec.ID); err == nil {
		for _, row := range rows {
			recipients[uuidToString(row.MemberID)] = row.MemberID
		}
	} else {
		slog.Warn("notifySpecApproval: list spec subscribers failed",
			"spec_id", uuidToString(spec.ID), "error", err)
	}

	// Linked issues contribute their own subscriber lists. Junction rows are
	// spec-scoped, so no workspace guard is needed here.
	if links, err := h.Queries.ListSpecIssues(ctx, spec.ID); err == nil {
		for _, link := range links {
			subs, err := h.Queries.ListIssueSubscribers(ctx, link.IssueID)
			if err != nil {
				slog.Warn("notifySpecApproval: list issue subscribers failed",
					"issue_id", uuidToString(link.IssueID), "error", err)
				continue
			}
			for _, sub := range subs {
				if sub.UserType != "member" {
					continue
				}
				recipients[uuidToString(sub.UserID)] = sub.UserID
			}
		}
	} else {
		slog.Warn("notifySpecApproval: list linked issues failed",
			"spec_id", uuidToString(spec.ID), "error", err)
	}

	detailsMap := map[string]string{
		"spec_id":        uuidToString(spec.ID),
		"identifier":     spec.Identifier,
		"version_id":     uuidToString(version.ID),
		"version_number": fmt.Sprintf("%d", version.Number),
		"changelog":      version.Changelog,
	}
	details, _ := json.Marshal(detailsMap)

	workspaceID := uuidToString(spec.WorkspaceID)
	var actorUUID pgtype.UUID
	if actorID != "" {
		if u, err := util.ParseUUID(actorID); err == nil {
			actorUUID = u
		}
	}
	for userID, userUUID := range recipients {
		if userID == actorID {
			continue
		}
		item, err := h.Queries.CreateInboxItem(ctx, db.CreateInboxItemParams{
			WorkspaceID:   spec.WorkspaceID,
			RecipientType: "member",
			RecipientID:   userUUID,
			Type:          "spec_approved",
			Severity:      "info",
			Title:         spec.Title,
			Body:          strToText(version.Changelog),
			ActorType:     strToText(actorType),
			ActorID:       actorUUID,
			Details:       details,
		})
		if err != nil {
			slog.Error("spec approval notification creation failed",
				"subscriber_id", userID, "error", err)
			continue
		}
		h.publish(protocol.EventInboxNew, workspaceID, actorType, actorID, map[string]any{"item": inboxToResponse(item)})
	}
}
