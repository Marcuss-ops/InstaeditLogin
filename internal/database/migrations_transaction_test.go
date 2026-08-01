package database

import "testing"

func TestMigrationSQLForTransactionRemovesOnlyOuterWrappers(t *testing.T) {
	body := `-- preserve BEGIN; in a comment
BEGIN;

DO $$
BEGIN;
  PERFORM 1;
  COMMIT;
END $$;

SELECT 'BEGIN; and COMMIT; are string contents';
COMMIT;
-- preserve COMMIT; in a trailing comment
`

	got := migrationSQLForTransaction(body)
	want := `-- preserve BEGIN; in a comment

DO $$
BEGIN;
  PERFORM 1;
  COMMIT;
END $$;

SELECT 'BEGIN; and COMMIT; are string contents';
-- preserve COMMIT; in a trailing comment
`
	if got != want {
		t.Fatalf("migrationSQLForTransaction() = %q, want %q", got, want)
	}
}
