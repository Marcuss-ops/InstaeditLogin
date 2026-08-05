package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestUserRepository_DisconnectPlatformAccount_SharedGrant_SequentialLifecycle
// pins the P1 shared-grant guarantee across a full lifecycle: A and B share
// one oauth_connection (grant 55). Disconnecting A first while B is still
// active must NOT report lastOnGrant — the caller then skips both the remote
// and the local vault revoke, so B keeps working. Only disconnecting the
// last channel B reports lastOnGrant=true so the caller revokes the grant
// exactly once.
func TestUserRepository_DisconnectPlatformAccount_SharedGrant_SequentialLifecycle(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	// A (21) disconnects first: B (22) still active on the grant.
	expectDisconnectTransaction(mock, 21, 55, 1)
	// B (22) disconnects last: no active sibling remains.
	expectDisconnectTransaction(mock, 22, 55, 0)

	lastOnGrantA, handledA, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("disconnect A: %v", err)
	}
	if !handledA {
		t.Fatal("disconnect A: handled = false, want true")
	}
	if lastOnGrantA {
		t.Fatal("disconnect A: lastOnGrant must be false while sibling B is still active (the shared grant must survive)")
	}

	lastOnGrantB, handledB, err := repo.DisconnectPlatformAccount(context.Background(), 22)
	if err != nil {
		t.Fatalf("disconnect B: %v", err)
	}
	if !handledB {
		t.Fatal("disconnect B: handled = false, want true")
	}
	if !lastOnGrantB {
		t.Fatal("disconnect B: lastOnGrant must be true (last channel) so the caller revokes the grant")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

// TestUserRepository_PermanentlyDeleteAccount_SharedGrant_SequentialLifecycle
// pins the hard-delete guarantee: deleting A while B is still active must
// preserve the grant AND its shared tokens (the revoke callback is NOT
// invoked and no token/connection DELETE is issued); deleting the last
// channel B revokes the grant, purges its tokens and removes the
// oauth_connections row — exactly once across the whole lifecycle.
func TestUserRepository_PermanentlyDeleteAccount_SharedGrant_SequentialLifecycle(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	// A (21) first: B (22) still active → grant + tokens preserved.
	expectPermanentDeleteTransaction(mock, 21, 55, 1, nil)
	// B (22) last: grant removed.
	expectPermanentDeleteTransaction(mock, 22, 55, 0, nil)

	revokeCalls := 0
	revoke := func(context.Context, *sql.Tx) error {
		revokeCalls++
		return nil
	}

	handledA, err := repo.PermanentlyDeleteAccountTx(context.Background(), 21, revoke)
	if err != nil {
		t.Fatalf("hard delete A: %v", err)
	}
	if !handledA {
		t.Fatal("hard delete A: handled = false, want true")
	}
	if revokeCalls != 0 {
		t.Fatalf("hard delete A: revoke callback must NOT run while sibling B is active (got %d calls)", revokeCalls)
	}

	handledB, err := repo.PermanentlyDeleteAccountTx(context.Background(), 22, revoke)
	if err != nil {
		t.Fatalf("hard delete B: %v", err)
	}
	if !handledB {
		t.Fatal("hard delete B: handled = false, want true")
	}
	if revokeCalls != 1 {
		t.Fatalf("hard delete B: revoke callback must run exactly once for the last channel (got %d calls)", revokeCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
