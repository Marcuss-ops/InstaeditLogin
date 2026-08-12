package services

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// PublicationResolverStore is deliberately read-only. Preview and the
// preparation worker use the same resolver contract, so neither can invent a
// different precedence rule for metadata or translations.
type PublicationResolverStore interface {
	FindPackage(ctx context.Context, workspaceID, packageID int64) (*models.ContentPackage, error)
	ListTargets(ctx context.Context, packageID int64) ([]*models.ContentPackageTarget, error)
	FindCurrentMetadata(ctx context.Context, packageID int64) (*models.ContentMetadataRevision, error)
	ResolveTranslationEntries(ctx context.Context, packageID, revisionID int64, languages []string) (*models.TranslationBundle, map[string]*models.TranslationEntry, error)
}

type PublicationBlocker struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ResolvedPublication struct {
	Package             *models.ContentPackage
	Target              *models.ContentPackageTarget
	MetadataRevision    *models.ContentMetadataRevision
	TranslationBundleID *int64
	Title               string
	Description         string
	Tags                json.RawMessage
	PrivacyStatus       string
	ThumbnailMediaID    *string
	Blockers            []PublicationBlocker
	Warnings            []string
}

func (r ResolvedPublication) Ready() bool { return len(r.Blockers) == 0 }

type PublicationResolver struct{ store PublicationResolverStore }

func NewPublicationResolver(store PublicationResolverStore) *PublicationResolver {
	return &PublicationResolver{store: store}
}

func (r *PublicationResolver) Resolve(ctx context.Context, workspaceID, packageID, targetID int64) (*ResolvedPublication, error) {
	pkg, err := r.store.FindPackage(ctx, workspaceID, packageID)
	if err != nil {
		return nil, err
	}
	metadata, err := r.store.FindCurrentMetadata(ctx, packageID)
	if err != nil {
		return nil, err
	}
	targets, err := r.store.ListTargets(ctx, packageID)
	if err != nil {
		return nil, err
	}
	var target *models.ContentPackageTarget
	for _, candidate := range targets {
		if candidate.ID == targetID {
			target = candidate
			break
		}
	}
	if target == nil {
		return &ResolvedPublication{Package: pkg, MetadataRevision: metadata, Blockers: []PublicationBlocker{{Code: "target_missing", Message: "target is not configured for this content package"}}}, nil
	}
	resolved := &ResolvedPublication{
		Package: pkg, Target: target, MetadataRevision: metadata,
		Title: metadata.Title, Description: metadata.Description, Tags: metadata.Tags,
		PrivacyStatus: target.PrivacyStatus, ThumbnailMediaID: pkg.CurrentCoverMediaID,
	}
	if strings.TrimSpace(target.Language) == "" {
		resolved.Blockers = append(resolved.Blockers, PublicationBlocker{Code: "language_missing", Message: "target language is required"})
		return resolved, nil
	}
	if target.Language != metadata.SourceLanguage {
		bundle, entries, bundleErr := r.store.ResolveTranslationEntries(ctx, packageID, metadata.ID, []string{target.Language})
		if bundleErr != nil {
			return nil, bundleErr
		}
		entry := entries[target.Language]
		if entry == nil {
			resolved.Blockers = append(resolved.Blockers, PublicationBlocker{Code: "translation_missing", Message: "translation is missing for target language"})
		} else {
			resolved.Title, resolved.Description, resolved.Tags = entry.Title, entry.Description, entry.Tags
			if bundle != nil {
				resolved.TranslationBundleID = &bundle.ID
			}
		}
	}
	if strings.TrimSpace(resolved.Title) == "" {
		resolved.Blockers = append(resolved.Blockers, PublicationBlocker{Code: "title_missing", Message: "title is required"})
	}
	if resolved.ThumbnailMediaID == nil || strings.TrimSpace(*resolved.ThumbnailMediaID) == "" {
		resolved.Blockers = append(resolved.Blockers, PublicationBlocker{Code: "cover_missing", Message: "cover is missing"})
	}
	if strings.TrimSpace(pkg.DriveFileID) == "" {
		resolved.Blockers = append(resolved.Blockers, PublicationBlocker{Code: "drive_missing", Message: "Drive file reference is missing"})
	}
	return resolved, nil
}

func (r *PublicationResolver) ResolveAll(ctx context.Context, workspaceID, packageID int64) ([]*ResolvedPublication, error) {
	targets, err := r.store.ListTargets(ctx, packageID)
	if err != nil {
		return nil, err
	}
	out := make([]*ResolvedPublication, 0, len(targets))
	for _, target := range targets {
		if target == nil || !target.Enabled {
			continue
		}
		resolved, err := r.Resolve(ctx, workspaceID, packageID, target.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}
