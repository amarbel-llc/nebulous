package tools

import (
	"log"
	"sort"
)

type storyQuery struct {
	Words   []string
	Year    *int
	Month   *int
	Tag     *string
	FeedID  *int
	Starred *bool
	Read    *bool
	Offset  int
	Limit   int
}

func (s *storyStore) query(q storyQuery) []*storyRecord {
	recs, _, err := s.queryDetailed(q)
	if err != nil {
		log.Printf("story query: %v", err)
	}
	return recs
}

// queryDetailed is query plus what story_query's caller needs to know: a
// note about the word index's freshness (empty when current or unused) and
// a lookup error from the on-disk index, which query itself can only log.
func (s *storyStore) queryDetailed(q storyQuery) (results []*storyRecord, note string, err error) {
	if q.Limit == 0 {
		q.Limit = 100
	}

	snap, err := s.current()
	if err != nil || snap == nil {
		return nil, "", err
	}

	var candidates []*storyRecord
	usedWords := len(q.Words) > 0

	if usedWords && snap.index != nil {
		note = snap.indexNote
		candidates, err = snap.diskCandidates(q.Words)
		if err != nil {
			return nil, note, err
		}
	} else if usedWords {
		seen := make(map[string]bool)
		for _, word := range q.Words {
			for _, rec := range snap.words[word] {
				if !seen[rec.Hash] {
					seen[rec.Hash] = true
					candidates = append(candidates, rec)
				}
			}
		}
	} else {
		candidates = snap.stories
	}

	var filtered []*storyRecord
	for _, rec := range candidates {
		if q.Year != nil && rec.Year != *q.Year {
			continue
		}
		if q.Month != nil && rec.Month != *q.Month {
			continue
		}
		if q.Tag != nil {
			found := false
			for _, t := range rec.UserTags {
				if t == *q.Tag {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		if q.FeedID != nil && rec.FeedID != *q.FeedID {
			continue
		}
		if q.Starred != nil && rec.Starred != *q.Starred {
			continue
		}
		if q.Read != nil && rec.Read != *q.Read {
			continue
		}
		filtered = append(filtered, rec)
	}

	if usedWords {
		sort.Slice(filtered, func(i, j int) bool {
			return filtered[i].Date.After(filtered[j].Date)
		})
	}

	if q.Offset >= len(filtered) {
		return nil, note, nil
	}
	filtered = filtered[q.Offset:]
	if len(filtered) > q.Limit {
		filtered = filtered[:q.Limit]
	}

	return filtered, note, nil
}
