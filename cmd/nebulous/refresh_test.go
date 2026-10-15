package main

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"code.linenisgreat.com/nebulous/internal/alfa/newsblur"
)

var refreshNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func TestPlanRefreshIncrementalFetchesOnlyChangedStamps(t *testing.T) {
	state := newsblur.StarredSyncState{
		Timestamps: map[string]string{"a": "1", "b": "2", "c": "3"},
		FullSyncAt: refreshNow.Add(-time.Hour),
	}
	live := map[string]string{"a": "1", "b": "9", "c": "3", "d": "4"}

	plan := planRefresh(state, true, live, nil, false, 24*time.Hour, refreshNow)
	if plan.Full {
		t.Error("plan.Full = true, want incremental")
	}
	if want := []string{"b", "d"}; !reflect.DeepEqual(plan.ToFetch, want) {
		t.Errorf("ToFetch = %v, want %v", plan.ToFetch, want)
	}
}

func TestPlanRefreshFullWhenFirstRunForcedOrDue(t *testing.T) {
	live := map[string]string{"a": "1", "b": "2"}
	recent := newsblur.StarredSyncState{Timestamps: live, FullSyncAt: refreshNow.Add(-time.Hour)}

	for name, tc := range map[string]struct {
		state     newsblur.StarredSyncState
		have      bool
		forceFull bool
	}{
		"no recorded state":      {newsblur.StarredSyncState{}, false, false},
		"state never fully done": {newsblur.StarredSyncState{Timestamps: live}, true, false},
		"forced":                 {recent, true, true},
		"interval elapsed":       {newsblur.StarredSyncState{Timestamps: live, FullSyncAt: refreshNow.Add(-48 * time.Hour)}, true, false},
	} {
		plan := planRefresh(tc.state, tc.have, live, nil, tc.forceFull, 24*time.Hour, refreshNow)
		if !plan.Full || !reflect.DeepEqual(plan.ToFetch, []string{"a", "b"}) {
			t.Errorf("%s: plan = %+v, want a full re-sync of a and b", name, plan)
		}
	}
}

func TestPlanRefreshSkipsStoriesJustFetched(t *testing.T) {
	live := map[string]string{"a": "1", "b": "2"}
	plan := planRefresh(newsblur.StarredSyncState{}, false, live, map[string]bool{"a": true}, false, time.Hour, refreshNow)
	if want := []string{"b"}; !reflect.DeepEqual(plan.ToFetch, want) {
		t.Errorf("ToFetch = %v, want %v", plan.ToFetch, want)
	}
}

func TestDeletedTagHoldersFindsOnlyTagsNewsBlurNoLongerLists(t *testing.T) {
	cached := map[string][]string{
		"1:a": {"kept", "gone"},
		"2:b": {"gone"},
		"3:c": {"", "padded "},
		"4:d": {"kept"},
	}
	live := map[string]int{"kept": 2}

	got := deletedTagHolders(cached, live)
	want := map[string][]string{"gone": {"1:a", "2:b"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("holders = %v, want %v (empty and untrimmed tags are skipped)", got, want)
	}
}

type fakeRefresh struct {
	stamps  map[string]string
	counts  map[string]int
	cached  map[string][]string
	state   newsblur.StarredSyncState
	have    bool
	fetched [][]string
	failAt  int // fail the Nth Fetch call (1-based); 0 never
}

func (f *fakeRefresh) deps() refreshDeps {
	return refreshDeps{
		LiveStamps:   func(context.Context) (map[string]string, error) { return f.stamps, nil },
		LiveTagCount: func(context.Context) (map[string]int, error) { return f.counts, nil },
		LoadState:    func() (newsblur.StarredSyncState, bool) { return f.state, f.have },
		SaveState: func(s newsblur.StarredSyncState) error {
			f.state, f.have = s, true
			return nil
		},
		Fetch: func(_ context.Context, hashes []string) (int, error) {
			f.fetched = append(f.fetched, slices.Clone(hashes))
			if f.failAt != 0 && len(f.fetched) == f.failAt {
				return 0, errors.New("boom")
			}
			return len(hashes), nil
		},
		CachedTags: func() (map[string][]string, error) { return f.cached, nil },
		Now:        func() time.Time { return refreshNow },
	}
}

func TestRunRefreshFirstRunDownloadsEverythingAndRecordsState(t *testing.T) {
	f := &fakeRefresh{
		stamps: map[string]string{"a": "1", "b": "2", "c": "3"},
		counts: map[string]int{},
		cached: map[string][]string{},
	}
	n, err := runRefresh(context.Background(), f.deps(), refreshOpts{FullEvery: time.Hour, JustFetched: map[string]bool{"c": true}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || !reflect.DeepEqual(f.fetched, [][]string{{"a", "b"}}) {
		t.Errorf("n = %d, fetched = %v, want a and b only (c was just fetched)", n, f.fetched)
	}
	if want := map[string]string{"a": "1", "b": "2", "c": "3"}; !reflect.DeepEqual(f.state.Timestamps, want) {
		t.Errorf("timestamps = %v, want %v", f.state.Timestamps, want)
	}
	if !f.state.FullSyncAt.Equal(refreshNow) {
		t.Errorf("FullSyncAt = %v, want %v", f.state.FullSyncAt, refreshNow)
	}
}

func TestRunRefreshSecondRunWithNothingChangedDownloadsNothing(t *testing.T) {
	f := &fakeRefresh{
		stamps: map[string]string{"a": "1"},
		counts: map[string]int{"t": 1},
		cached: map[string][]string{"a": {"t"}},
		state:  newsblur.StarredSyncState{Timestamps: map[string]string{"a": "1"}, FullSyncAt: refreshNow.Add(-time.Minute)},
		have:   true,
	}
	n, err := runRefresh(context.Background(), f.deps(), refreshOpts{FullEvery: time.Hour})
	if err != nil || n != 0 || len(f.fetched) != 0 {
		t.Errorf("n = %d, err = %v, fetched = %v, want nothing downloaded", n, err, f.fetched)
	}
}

func TestRunRefreshDropsUnstarredStoriesFromTheRecord(t *testing.T) {
	f := &fakeRefresh{
		stamps: map[string]string{"a": "1"},
		counts: map[string]int{},
		cached: map[string][]string{},
		state:  newsblur.StarredSyncState{Timestamps: map[string]string{"a": "1", "gone": "7"}, FullSyncAt: refreshNow},
		have:   true,
	}
	if _, err := runRefresh(context.Background(), f.deps(), refreshOpts{FullEvery: time.Hour}); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.state.Timestamps["gone"]; ok {
		t.Errorf("timestamps = %v, want the unstarred story dropped", f.state.Timestamps)
	}
}

func TestRunRefreshRedownloadsHoldersOfATagDeletedInTheApps(t *testing.T) {
	f := &fakeRefresh{
		stamps: map[string]string{"a": "1", "b": "2"},
		counts: map[string]int{"kept": 1},
		cached: map[string][]string{"a": {"gone"}, "b": {"kept"}},
		state:  newsblur.StarredSyncState{Timestamps: map[string]string{"a": "1", "b": "2"}, FullSyncAt: refreshNow},
		have:   true,
	}
	n, err := runRefresh(context.Background(), f.deps(), refreshOpts{FullEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !reflect.DeepEqual(f.fetched, [][]string{{"a"}}) {
		t.Errorf("n = %d, fetched = %v, want only the holder of the deleted tag", n, f.fetched)
	}
}

func TestRunRefreshFailureKeepsTheStampsSavedSoFar(t *testing.T) {
	stamps := map[string]string{}
	for i := 0; i < refreshChunk+10; i++ {
		stamps["h"+strconv.Itoa(1000+i)] = "1"
	}
	f := &fakeRefresh{stamps: stamps, counts: map[string]int{}, cached: map[string][]string{}, failAt: 2}

	n, err := runRefresh(context.Background(), f.deps(), refreshOpts{FullEvery: time.Hour})
	if err == nil {
		t.Fatal("err = nil, want the failure")
	}
	if n != refreshChunk {
		t.Errorf("n = %d, want %d (the first chunk completed)", n, refreshChunk)
	}
	if got := len(f.state.Timestamps); got != refreshChunk {
		t.Errorf("recorded %d stamps, want %d so the next run resumes after the first chunk", got, refreshChunk)
	}
	if !f.state.FullSyncAt.IsZero() {
		t.Error("FullSyncAt recorded for an interrupted full re-sync")
	}
}
