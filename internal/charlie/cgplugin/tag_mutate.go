package cgplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"code.linenisgreat.com/purse-first/libs/dewey/pkgs/errors"
)

// tagPatchBody is PatchNode's payload for a tag node (newsblur://tag/<x>):
// rename the tag on every story that carries it. Force allows the new name
// to be a tag that already exists, which merges the two (a rename is
// otherwise refused, since the merge cannot be undone).
type tagPatchBody struct {
	Name  *string `json:"name,omitempty"`
	Force bool    `json:"force,omitempty"`
}

// userTagHolders lists the stories in the local index that carry tag as a
// USER tag. The tag nodes this plugin lists also include feed-assigned story
// tags, which are not editable: only user tags can be renamed or deleted.
func userTagHolders(tag string) ([]string, error) {
	refs, err := index.StoriesByTag(tag)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for _, s := range refs {
		if slices.Contains(s.UserTags, tag) {
			hashes = append(hashes, s.Hash)
		}
	}
	return hashes, nil
}

// patchTag renames a user tag across every story that carries it. NewsBlur,
// not the local index, is the authority on whether the tag (and the new
// name) exist: the index only learns about tags changed through this
// process, so the existence checks read NewsBlur's own counts.
func patchTag(ctx context.Context, tag string, raw []byte) ([]string, error) {
	var patch tagPatchBody
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, errors.BadRequestf("newsblur plugin: invalid tag patch body: %w", err)
	}
	if patch.Name == nil {
		return []string{}, nil
	}
	newName := *patch.Name
	if err := checkTag(newName); err != nil {
		return nil, err
	}
	if newName == tag {
		return nil, errors.BadRequestf("newsblur plugin: tag %q already has that name", tag)
	}

	holders, err := userTagHolders(tag)
	if err != nil {
		return nil, err
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()

	live, err := client.LiveStarredTagCounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("newsblur plugin: reading NewsBlur's tag counts: %w", err)
	}
	if _, ok := live[tag]; !ok {
		return nil, errors.BadRequestf("newsblur plugin: %q is not a tag on any story in NewsBlur", tag)
	}
	// NewsBlur merges case-insensitively (its rename_tag compares lowercased
	// names), so "Other" counts as an existing tag for a rename to "other".
	// Renaming a tag to a different casing of ITSELF is not a merge.
	if !patch.Force {
		for existing, n := range live {
			if existing != tag && strings.EqualFold(existing, newName) {
				return nil, errors.BadRequestf(
					"newsblur plugin: a tag named %q already exists (%d stories); renaming %q to %q would merge the two (NewsBlur compares tag names case-insensitively); pass \"force\": true to merge",
					existing, n, tag, newName)
			}
		}
	}

	if _, err := client.RenameStarredTag(ctx, tag, newName); err != nil {
		return nil, fmt.Errorf("newsblur plugin: renaming tag %q: %w", tag, err)
	}
	_ = client.PatchCachedUserTag(holders, tag, newName)
	return []string{"name"}, nil
}

// deleteTag removes a user tag from every story that carries it; the
// stories stay starred. It is destructive for every story at once, so NewsBlur
// must confirm the tag exists first.
func deleteTag(ctx context.Context, tag string) error {
	holders, err := userTagHolders(tag)
	if err != nil {
		return err
	}

	mutateMu.Lock()
	defer mutateMu.Unlock()

	live, err := client.LiveStarredTagCounts(ctx)
	if err != nil {
		return fmt.Errorf("newsblur plugin: reading NewsBlur's tag counts: %w", err)
	}
	if _, ok := live[tag]; !ok {
		return errors.BadRequestf("newsblur plugin: %q is not a tag on any story in NewsBlur", tag)
	}
	if _, err := client.DeleteStarredTag(ctx, tag); err != nil {
		return fmt.Errorf("newsblur plugin: deleting tag %q: %w", tag, err)
	}
	_ = client.PatchCachedUserTag(holders, tag, "")
	return nil
}
