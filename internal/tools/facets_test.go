package tools

import (
	"encoding/json"
	"testing"
)

func TestStoryFacets(t *testing.T) {
	store := makeTestStore()
	facets := store.facets()

	if facets.TotalStories != 4 {
		t.Errorf("TotalStories = %d, want 4", facets.TotalStories)
	}
	if facets.ByYear[2024] != 3 {
		t.Errorf("ByYear[2024] = %d, want 3", facets.ByYear[2024])
	}
	if facets.ByYear[2023] != 1 {
		t.Errorf("ByYear[2023] = %d, want 1", facets.ByYear[2023])
	}
	if facets.ByTag["interests"] != 2 {
		t.Errorf("ByTag[interests] = %d, want 2", facets.ByTag["interests"])
	}
	if facets.ByTag["zz-nyc"] != 1 {
		t.Errorf("ByTag[zz-nyc] = %d, want 1", facets.ByTag["zz-nyc"])
	}
	if facets.ByFeed[1].Count != 2 {
		t.Errorf("ByFeed[1].Count = %d, want 2", facets.ByFeed[1].Count)
	}
	if facets.ByStatus["starred"] != 4 {
		t.Errorf("ByStatus[starred] = %d, want 4", facets.ByStatus["starred"])
	}
	if len(facets.Years) != 2 || facets.Years[0] != 2024 {
		t.Errorf("Years = %v, want [2024 2023]", facets.Years)
	}

	// Verify JSON serialization works
	data, err := json.Marshal(facets)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if len(data) == 0 {
		t.Error("empty JSON")
	}
}
