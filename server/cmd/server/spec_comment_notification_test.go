package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestSpecCommentNotifications covers the comment:created listener's spec
// branch: a spec comment notifies spec subscribers (not issue subscribers)
// and parses member mentions into issue-less inbox items.
func TestSpecCommentNotifications(t *testing.T) {
	queries := db.New(testPool)
	bus := newNotificationBus(t, queries)
	ctx := context.Background()

	// Identifiers are free TEXT unique per workspace; a nanosecond suffix
	// keeps reruns of this test from colliding.
	specIdentifier := fmt.Sprintf("NOTIF-S-%d", time.Now().UnixNano())
	var specID, versionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO spec (workspace_id, title, identifier, status, created_by)
		VALUES ($1, 'Notification spec', $2, 'draft', $3)
		RETURNING id
	`, testWorkspaceID, specIdentifier, testUserID).Scan(&specID); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO spec_version (spec_id, number, content, changelog, created_by)
		VALUES ($1, 1, 'body', '', $2)
		RETURNING id
	`, specID, testUserID).Scan(&versionID); err != nil {
		t.Fatalf("insert spec version: %v", err)
	}
	specTitle := "Notification spec"
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM inbox_item WHERE workspace_id = $1 AND title = $2`, testWorkspaceID, specIdentifier+": "+specTitle)
		testPool.Exec(ctx, `DELETE FROM spec WHERE id = $1`, specID)
	})

	mentionedEmail := "spec-comment-mentioned@multica.ai"
	mentionedID := createTestUser(t, mentionedEmail)
	t.Cleanup(func() { cleanupTestUser(t, mentionedEmail) })
	subscriberEmail := "spec-comment-subscriber@multica.ai"
	subscriberID := createTestUser(t, subscriberEmail)
	t.Cleanup(func() { cleanupTestUser(t, subscriberEmail) })

	if _, err := testPool.Exec(ctx, `
		INSERT INTO spec_subscriber (spec_id, member_id)
		VALUES ($1, $2)
	`, specID, subscriberID); err != nil {
		t.Fatalf("insert spec subscriber: %v", err)
	}

	content := "[@Mentioned](mention://member/" + mentionedID + ") please review"
	bus.Publish(specCommentCreatedEvent(specID, versionID, specIdentifier, specTitle, content))

	t.Run("mentioned member gets an issue-less mentioned inbox item", func(t *testing.T) {
		items := inboxItemsForRecipient(t, queries, mentionedID)
		var found int
		for _, item := range items {
			if item.Type != "mentioned" {
				continue
			}
			found++
			if item.IssueID.Valid {
				t.Fatalf("mentioned inbox item has issue_id %v, want NULL", item.IssueID)
			}
			if !strings.Contains(item.Title, specIdentifier) {
				t.Fatalf("mentioned inbox title = %q, want spec identifier in it", item.Title)
			}
			// Mention items carry no body, on issue comments or spec ones.
			if item.Body.Valid && item.Body.String != "" {
				t.Fatalf("mentioned inbox body = %q, want empty", item.Body.String)
			}
			if !strings.Contains(string(item.Details), versionID) {
				t.Fatalf("mentioned inbox details = %q, want spec_version_id %s", item.Details, versionID)
			}
		}
		if found != 1 {
			t.Fatalf("mentioned inbox items = %d, want 1 (all items: %+v)", found, items)
		}
	})

	t.Run("spec subscriber gets an issue-less new_comment inbox item", func(t *testing.T) {
		items := inboxItemsForRecipient(t, queries, subscriberID)
		var found int
		for _, item := range items {
			if item.Type != "new_comment" {
				continue
			}
			found++
			if item.IssueID.Valid {
				t.Fatalf("subscriber inbox item has issue_id %v, want NULL", item.IssueID)
			}
			if !strings.Contains(item.Title, specIdentifier) {
				t.Fatalf("subscriber inbox title = %q, want spec identifier in it", item.Title)
			}
		}
		if found != 1 {
			t.Fatalf("subscriber new_comment inbox items = %d, want 1 (all items: %+v)", found, items)
		}
	})

	t.Run("actor gets no inbox item", func(t *testing.T) {
		// The shared workspace keeps other tests' items for the same actor, so
		// scope to what THIS event could have produced: any spec-comment
		// notification carries spec_id in its details.
		for _, item := range inboxItemsForRecipient(t, queries, testUserID) {
			if strings.Contains(string(item.Details), specID) {
				t.Fatalf("actor inbox item for own spec comment = %+v, want none", item)
			}
		}
	})
}

// specCommentCreatedEvent builds the exact comment:created payload the spec
// comment handler publishes.
func specCommentCreatedEvent(specID, versionID, identifier, title, content string) events.Event {
	return events.Event{
		Type:        protocol.EventCommentCreated,
		WorkspaceID: testWorkspaceID,
		ActorType:   "member",
		ActorID:     testUserID,
		Payload: map[string]any{
			"comment": handler.CommentResponse{
				ID:            "11111111-1111-4111-8111-111111111111",
				IssueID:       "",
				AuthorType:    "member",
				AuthorID:      testUserID,
				Content:       content,
				Type:          "comment",
				SpecVersionID: versionID,
			},
			"spec_id":             specID,
			"spec_version_id":     versionID,
			"spec_identifier":     identifier,
			"spec_title":          title,
			"spec_version_number": 1,
		},
	}
}

// TestReactionAddedWithoutIssueDoesNotNotify guards the reaction:added
// listener against the pre-DAWE-8 panic: a reaction on a spec comment carries
// an empty issue_id, and the listener must skip the inbox write instead of
// parseUUID("") panicking.
func TestReactionAddedWithoutIssueDoesNotNotify(t *testing.T) {
	queries := db.New(testPool)
	bus := newNotificationBus(t, queries)

	// Snapshot the actor's pre-existing items: the shared workspace keeps
	// other tests' items, and the assertion is that THIS event adds none.
	before := map[string]bool{}
	for _, item := range inboxItemsForRecipient(t, queries, testUserID) {
		before[util.UUIDToString(item.ID)] = true
	}

	bus.Publish(events.Event{
		Type:        protocol.EventReactionAdded,
		WorkspaceID: testWorkspaceID,
		ActorType:   "member",
		ActorID:     testUserID,
		Payload: map[string]any{
			"reaction": handler.ReactionResponse{
				ID:        "22222222-2222-4222-8222-222222222222",
				CommentID: "11111111-1111-4111-8111-111111111111",
				ActorType: "member",
				ActorID:   testUserID,
				Emoji:     "👍",
			},
			// No issue_id, issue_title, or issue_status — the spec-comment shape.
			"comment_id":          "11111111-1111-4111-8111-111111111111",
			"comment_author_type": "member",
			"comment_author_id":   testUserID,
			"comment_revision":    1,
		},
	})

	if items := inboxItemsForRecipient(t, queries, testUserID); len(items) != len(before) {
		for _, item := range items {
			if !before[util.UUIDToString(item.ID)] {
				t.Fatalf("new inbox item after issue-less reaction:added = %+v, want none", item)
			}
		}
	}
}
