package tools

import (
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
	if q.Limit == 0 {
		q.Limit = 100
	}

	var candidates []*storyRecord
	usedWords := len(q.Words) > 0

	if usedWords {
		seen := make(map[string]bool)
		for _, word := range q.Words {
			for _, rec := range s.words[word] {
				if !seen[rec.Hash] {
					seen[rec.Hash] = true
					candidates = append(candidates, rec)
				}
			}
		}
	} else {
		candidates = s.stories
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
		return nil
	}
	filtered = filtered[q.Offset:]
	if len(filtered) > q.Limit {
		filtered = filtered[:q.Limit]
	}

	return filtered
}
