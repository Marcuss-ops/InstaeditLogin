-- 098_thumbnail_project_revisions_immutable.sql
--
-- Revision rows are an append-only audit trail. Application code only inserts
-- revisions, but this invariant must also hold for direct SQL, maintenance
-- scripts, and compromised application paths. Parent project/workspace
-- cascades remain allowed so normal tenant cleanup is not blocked.

CREATE OR REPLACE FUNCTION prevent_thumbnail_project_revision_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE'
       AND NOT EXISTS (
           SELECT 1
             FROM thumbnail_projects
            WHERE id = OLD.project_id
       ) THEN
        RETURN OLD;
    END IF;

    RAISE EXCEPTION 'thumbnail_project_revisions are immutable; use a new revision'
        USING ERRCODE = 'restrict_violation';
END;
$$;

DROP TRIGGER IF EXISTS thumbnail_project_revisions_immutable_trg
    ON thumbnail_project_revisions;

CREATE TRIGGER thumbnail_project_revisions_immutable_trg
    BEFORE UPDATE OR DELETE ON thumbnail_project_revisions
    FOR EACH ROW
    EXECUTE FUNCTION prevent_thumbnail_project_revision_mutation();
