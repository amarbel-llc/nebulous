package tools

import "sort"

type feedFacetEntry struct {
	Count int    `json:"count"`
	Title string `json:"title,omitempty"`
}

type storyFacets struct {
	TotalStories int                     `json:"total_stories"`
	ByYear       map[int]int             `json:"by_year"`
	ByTag        map[string]int          `json:"by_tag"`
	ByFeed       map[int]*feedFacetEntry `json:"by_feed"`
	ByStatus     map[string]int          `json:"by_status"`
	Years        []int                   `json:"years"`
}

func (s *storyStore) facets() *storyFacets {
	f := &storyFacets{
		ByYear:   make(map[int]int),
		ByTag:    make(map[string]int),
		ByFeed:   make(map[int]*feedFacetEntry),
		ByStatus: make(map[string]int),
	}

	for _, rec := range s.stories {
		f.TotalStories++

		f.ByYear[rec.Year]++

		for _, tag := range rec.UserTags {
			f.ByTag[tag]++
		}

		entry, ok := f.ByFeed[rec.FeedID]
		if !ok {
			entry = &feedFacetEntry{Title: rec.Title}
			f.ByFeed[rec.FeedID] = entry
		}
		entry.Count++

		if rec.Starred {
			f.ByStatus["starred"]++
		}
		if rec.Read {
			f.ByStatus["read"]++
		} else {
			f.ByStatus["unread"]++
		}
	}

	for year := range f.ByYear {
		f.Years = append(f.Years, year)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(f.Years)))

	return f
}
