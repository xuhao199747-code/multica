ALTER TABLE project_execution_issue
    DROP CONSTRAINT project_execution_issue_role_check,
    ADD CONSTRAINT project_execution_issue_role_check
        CHECK (role IN ('development', 'review', 'repair', 'delivery'));
