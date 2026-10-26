ALTER TABLE project_execution_issue
    DROP CONSTRAINT project_execution_issue_role_check,
    ADD CONSTRAINT project_execution_issue_role_check
        CHECK (role IN ('main', 'development', 'review', 'repair', 'delivery'));
