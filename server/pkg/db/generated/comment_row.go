package db

// Comment converts the sqlc row returned by the CTE-backed create query into
// the canonical model used by comment rendering and task side effects. The
// row's IssueRevision remains available separately for owner-cache coherence.
func (r CreateCommentRow) Comment() Comment {
	return Comment{
		ID:                 r.ID,
		IssueID:            r.IssueID,
		AuthorType:         r.AuthorType,
		AuthorID:           r.AuthorID,
		Content:            r.Content,
		Type:               r.Type,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		ParentID:           r.ParentID,
		WorkspaceID:        r.WorkspaceID,
		ResolvedAt:         r.ResolvedAt,
		ResolvedByType:     r.ResolvedByType,
		ResolvedByID:       r.ResolvedByID,
		SourceTaskID:       r.SourceTaskID,
		QuickActionID:      r.QuickActionID,
		ViaPluginID:        r.ViaPluginID,
		Revision:           r.Revision,
		DeletedAt:          r.DeletedAt,
		SuppressedAgentIds: r.SuppressedAgentIds,
		SpecVersionID:      r.SpecVersionID,
	}
}

// Comment converts the sqlc row returned by the CTE-backed spec-comment
// create query into the canonical model. Spec comments have no issue anchor:
// IssueRevision is always 0 on this row.
func (r CreateSpecVersionCommentRow) Comment() Comment {
	return Comment{
		ID:                 r.ID,
		IssueID:            r.IssueID,
		AuthorType:         r.AuthorType,
		AuthorID:           r.AuthorID,
		Content:            r.Content,
		Type:               r.Type,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		ParentID:           r.ParentID,
		WorkspaceID:        r.WorkspaceID,
		ResolvedAt:         r.ResolvedAt,
		ResolvedByType:     r.ResolvedByType,
		ResolvedByID:       r.ResolvedByID,
		SourceTaskID:       r.SourceTaskID,
		QuickActionID:      r.QuickActionID,
		ViaPluginID:        r.ViaPluginID,
		Revision:           r.Revision,
		DeletedAt:          r.DeletedAt,
		SuppressedAgentIds: r.SuppressedAgentIds,
		SpecVersionID:      r.SpecVersionID,
	}
}

// Comment converts the sqlc row returned by the spec-wide list query into the
// canonical model. SpecVersionNumber is list metadata and stays on the row.
func (r ListCommentsForSpecRow) Comment() Comment {
	return Comment{
		ID:                 r.ID,
		IssueID:            r.IssueID,
		AuthorType:         r.AuthorType,
		AuthorID:           r.AuthorID,
		Content:            r.Content,
		Type:               r.Type,
		CreatedAt:          r.CreatedAt,
		UpdatedAt:          r.UpdatedAt,
		ParentID:           r.ParentID,
		WorkspaceID:        r.WorkspaceID,
		ResolvedAt:         r.ResolvedAt,
		ResolvedByType:     r.ResolvedByType,
		ResolvedByID:       r.ResolvedByID,
		SourceTaskID:       r.SourceTaskID,
		QuickActionID:      r.QuickActionID,
		ViaPluginID:        r.ViaPluginID,
		Revision:           r.Revision,
		DeletedAt:          r.DeletedAt,
		SuppressedAgentIds: r.SuppressedAgentIds,
		SpecVersionID:      r.SpecVersionID,
	}
}

// Comment converts the sqlc row returned by the CTE-backed update query into
// the canonical model. IssueRevision is aggregate-owner metadata and remains
// available on the row for response/event cache reconciliation.
func (r UpdateCommentRow) Comment() Comment {
	return Comment{
		ID:             r.ID,
		IssueID:        r.IssueID,
		AuthorType:     r.AuthorType,
		AuthorID:       r.AuthorID,
		Content:        r.Content,
		Type:           r.Type,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
		ParentID:       r.ParentID,
		WorkspaceID:    r.WorkspaceID,
		ResolvedAt:     r.ResolvedAt,
		ResolvedByType: r.ResolvedByType,
		ResolvedByID:   r.ResolvedByID,
		SourceTaskID:   r.SourceTaskID,
		QuickActionID:  r.QuickActionID,
		ViaPluginID:    r.ViaPluginID,
		Revision:       r.Revision,
		DeletedAt:      r.DeletedAt,
		SpecVersionID:  r.SpecVersionID,
	}
}
