package repo

import (
	"context"
	"fmt"
	"strings"

	"waifuapi/internal/storage"
	"waifuapi/internal/store/models"
)

// ListCatalogueKeys returns every image's expected storage keys, keyed by
// object name, mapped to the image id.
//
// The keys come from storage.ObjectKeyCandidates rather than a single
// "{id}{extension}" reconstruction, because a PNG row is stored as "{id}.webp".
// Every PNG is converted at upload and served back at its .png path by the
// Worker, so a catalogue that expects only "{id}.png" reports all 1,162 of them
// as orphans in S3 - and the storage-diff page shows a delete button beside them.
//
// A row therefore contributes every key it could plausibly occupy. A stored
// object is an orphan only when it matches none of them, and a row is missing
// only when none of its keys are in the bucket.
func (r *Repo) ListCatalogueKeys(ctx context.Context) (map[string]int64, error) {
	rows, err := r.db.Query(ctx, `SELECT "Id", "Extension" FROM "Images"`)
	if err != nil {
		return nil, fmt.Errorf("list catalogue keys: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]int64)
	for rows.Next() {
		var id int64
		var extension string
		if err := rows.Scan(&id, &extension); err != nil {
			return nil, fmt.Errorf("scan catalogue key: %w", err)
		}
		for _, key := range storage.ObjectKeyCandidates(id, extension) {
			// First writer wins, so a key maps to the row claiming it. With
			// unique ids the only variable part is the extension, so this cannot
			// collide in practice.
			if _, taken := keys[key]; !taken {
				keys[key] = id
			}
		}
	}
	return keys, rows.Err()
}

// CatalogueKeyFor picks the name to show an operator for a missing row.
//
// The catalogued extension is preferred over the storage fallback, because the
// operator knows the image by the extension the catalogue records, not by the
// one the object happens to be stored under. ObjectKeyCandidates lists the
// catalogued key first, so that is the one reported.
func CatalogueKeyFor(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	best := keys[0]
	for _, key := range keys {
		if !strings.HasSuffix(key, ".webp") {
			return key
		}
	}
	return best
}

// DeleteImages removes catalogue rows in bulk.
//
// Used by the storage reconciliation tool, which can select many rows at once.
// The stored objects are deliberately left alone: the operator chose this side of
// the reconciliation, and removing the object as well would make the action
// irreversible.
//
// Any pending report against a deleted image is resolved rather than left
// dangling. The report row survives its image — the foreign key is nulled on
// delete — so without this the reporter would be left waiting on a moderation
// decision about an image that no longer exists.
func (r *Repo) DeleteImages(ctx context.Context, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin image delete: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	const resolvedNote = "The reported image was deleted by a moderator."

	if _, err := tx.Exec(ctx, `
        UPDATE "Reports"
        SET "Status" = $1, "ReviewerNote" = $2
        WHERE "ImageId" = ANY($3) AND "Status" = $4`,
		int(models.ReportResolved), resolvedNote, ids,
		int(models.ReportPending)); err != nil {
		return 0, fmt.Errorf("resolve reports: %w", err)
	}

	tag, err := tx.Exec(ctx, `DELETE FROM "Images" WHERE "Id" = ANY($1)`, ids)
	if err != nil {
		return 0, fmt.Errorf("delete images: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit image delete: %w", err)
	}
	return tag.RowsAffected(), nil
}
