import re

path = "internal/repository/upload_job_pool_test.go"
with open(path, "r") as f:
    content = f.read()

# 1. Sostituisci il vecchio elenco di colonne con quello nuovo
old_cols = '''"id", "user_id", "workspace_id", "source_type", "source_id",
		"drive_account_id", "folder_id", "title", "caption",
		"targets", "status", "error_message", "post_id", "asset_id",
		"scheduled_at", "created_at", "updated_at",
		"attempt_count", "max_attempts", "next_attempt_at",
		"lease_owner", "lease_expires_at", "heartbeat_at",
		"progress_bytes", "total_bytes", "error_code", "priority",
		"started_at", "completed_at",'''

new_cols = '''"id", "user_id", "workspace_id", "source_type", "source_id",
		"drive_account_id", "folder_id", "title", "caption",
		"targets", "status", "error_message", "post_id", "asset_id",
		"ingest_after", "publish_at", "created_at", "updated_at",
		"attempt_count", "max_attempts", "next_attempt_at",
		"lease_owner", "lease_expires_at", "heartbeat_at",
		"progress_bytes", "total_bytes", "error_code", "priority",
		"started_at", "completed_at",
		"youtube_session_uri", "youtube_session_offset", "youtube_session_expires_at", "youtube_chunk_size", "youtube_last_chunk_at",
		"default_privacy_level"'''

print("Colonne vecchie trovate:", content.count(old_cols))
content = content.replace(old_cols, new_cols)

# 2. Stato ingest_completed invece di ready_to_publish
content = content.replace("status = 'ready_to_publish'", "status = 'ingest_completed'")
content = content.replace("status           = 'ready_to_publish'", "status           = 'ingest_completed'")

# 3. Colonne AggregateByFolder
old_agg = '''"pending_count", "retry_wait_count", "leased_count",
		"processing_count", "completed_count", "failed_count",
		"dead_letter_count", "cancelled_count",'''
new_agg = '''"pending_count", "retry_wait_count", "leased_count",
		"processing_count", "ready_to_publish_count", "completed_count", "failed_count",
		"dead_letter_count", "cancelled_count",'''
content = content.replace(old_agg, new_agg)
content = content.replace(
    ").AddRow(2, 1, 0, 0, 5, 1, 0, 0, nil, nil)",
    ").AddRow(2, 1, 0, 0, 0, 5, 1, 0, 0, nil, nil)",
    1,
)

# 4. Inserisci ingest_after e publish_at negli AddRow che avevano solo scheduled_at
content = content.replace(
    '''AddRow(101, 1, 1, "public_drive", "drive-file-1",
			nil, nil, "t1", "c1", []byte("[1,2]"), "leased", nil, nil, nil,
			nil, time.Now(), time.Now(),''',
    '''AddRow(101, 1, 1, "public_drive", "drive-file-1",
			nil, nil, "t1", "c1", []byte("[1,2]"), "leased", nil, nil, nil,
			nil, nil, time.Now(), time.Now(),''',
    1,
)
content = content.replace(
    '''AddRow(102, 1, 1, "public_drive", "drive-file-2",
			nil, nil, "t2", "c2", []byte("[3,4]"), "leased", nil, nil, nil,
			nil, time.Now(), time.Now(),''',
    '''AddRow(102, 1, 1, "public_drive", "drive-file-2",
			nil, nil, "t2", "c2", []byte("[3,4]"), "leased", nil, nil, nil,
			nil, nil, time.Now(), time.Now(),''',
    1,
)
content = content.replace(
    '''AddRow(201, 1, 1, "public_drive", "drive-file-201",
			nil, nil, "t201", "c201", []byte("[1,2]"), "leased", nil, nil, "asset-201",
			nil, time.Now(), time.Now(),''',
    '''AddRow(201, 1, 1, "public_drive", "drive-file-201",
			nil, nil, "t201", "c201", []byte("[1,2]"), "leased", nil, nil, "asset-201",
			nil, nil, time.Now(), time.Now(),''',
    1,
)

# 5. Aggiungi le colonne YouTube e default_privacy_level in fondo agli AddRow
# Per i primi due AddRow (ClaimBatch_Happy)
content = content.replace(
    '''			0, nil, nil, 100, time.Now(), nil,
		).''',
    '''			0, nil, nil, 100, time.Now(), nil,
			nil, nil, nil, nil, nil, "",
		).''',
    2,
)
# Per il terzo AddRow (ClaimBatchForPublish_Happy)
content = content.replace(
    '''			100, 100, nil, 100, time.Now(), nil,
		).''',
    '''			100, 100, nil, 100, time.Now(), nil,
			nil, nil, nil, nil, nil, "",
		).''',
    1,
)

with open(path, "w") as f:
    f.write(content)

print("Done")
